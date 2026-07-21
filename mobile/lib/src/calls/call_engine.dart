// A 1-to-1 voice call. A port of web/src/lib/call.ts.
//
// The media is peer to peer and never touches the server. What the server does is pass a handful of
// sealed envelopes between the two devices so they can find each other, and then get out of the way.
// Only when the two ends cannot reach each other at all does coturn relay the audio, and that is the
// one time call media transits our hardware.
//
// The signals are sealed under a key derived from the conversation's MLS group (call_envelope.dart).
// The server relays them and cannot read them, which matters because they carry the SDP, and the SDP
// carries the DTLS fingerprint that WebRTC's own media encryption is authenticated against. A server
// able to rewrite that fingerprint could put itself in the middle of the call. It cannot rewrite what
// it cannot read.
//
// A plain Dart class, deliberately: a call outlives any one screen — you can navigate away from the
// conversation and keep talking — and the microphone must be released exactly once no matter how the
// UI unmounts. See CallController for where it is held.

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_webrtc/flutter_webrtc.dart';

import '../core/api_exception.dart';
import '../crypto/mls_service.dart';
import '../crypto/mls_session.dart';
import '../data/pheme_repository.dart';
import 'call_envelope.dart';
import 'call_state.dart';
import 'proximity_lock.dart';

/// How long a call rings before it gives up.
const ringTimeout = Duration(seconds: 35);

/// How long to wait for ICE gathering before sending the offer anyway.
///
/// One complete SDP rather than trickling candidates as they arrive. Trickling is faster, but each
/// candidate is independently load-bearing and none of them has a next stage that would reveal it went
/// missing — so a lost candidate does not fail the call, it silently downgrades it to a TURN relay and
/// nobody notices except the bandwidth bill. One complete offer costs half a second and cannot
/// half-arrive.
const iceGatherTimeout = Duration(seconds: 2);

/// While a call is being set up, re-read the mailbox this often in case a nudge was dropped.
const pollInterval = Duration(milliseconds: 400);

/// How often the caller re-points the callee at a still-unanswered invite.
const reRingInterval = Duration(seconds: 3);

class CallEngine {
  CallEngine._({
    required this.conversationId,
    required this.callId,
    required this.outgoing,
    required this._userId,
    required PhemeRepository repository,
    required this._mls,
    required this._onChange,
  }) : _repo = repository,
       _status = outgoing ? CallStatus.calling : CallStatus.ringing;

  final String conversationId;
  final String callId;
  final bool outgoing;

  final String _userId;
  final PhemeRepository _repo;
  final MlsService _mls;
  final void Function(CallState) _onChange;

  CallStatus _status;
  CallEndReason? _reason;

  RTCPeerConnection? _pc;
  MediaStream? _localStream;
  MediaStream? _remoteStream;

  bool _muted = false;
  AudioRoute _route = AudioRoute.earpiece;

  DateTime? _connectedAt;
  DateTime? _endedAt;

  /// Our own identity and the key we seal with. Derived ONCE — see [_deriveKeys].
  String _identity = '';
  Uint8List? _secret;
  int _epoch = 0;

  /// The other devices' keys, by identity. Cached so each signal does not re-derive.
  final _peerSecrets = <String, Uint8List>{};

  int _seq = 0;

  /// The highest sequence seen from each sender, so a replayed signal is refused.
  final _seen = <String, int>{};

  /// How far through the mailbox we have read.
  int _cursor = 0;

  /// The invite's SDP, once we have it. On mobile this can arrive AFTER the phone starts ringing —
  /// iOS demands a call be reported to CallKit before any of this work can be done — so answering has
  /// to be able to wait for it.
  final _invite = Completer<String>();
  bool get inviteReady => _invite.isCompleted;

  void Function()? _releaseGroup;
  Timer? _pollTimer;
  Timer? _reRingTimer;
  Timer? _ringTimer;
  Timer? _tick;
  bool _draining = false;

  // --- lifecycle -------------------------------------------------------------------------------

  /// Places a call.
  static Future<CallEngine> place({
    required String conversationId,
    required String callId,
    required String userId,
    required PhemeRepository repository,
    required MlsService mls,
    required void Function(CallState) onChange,
  }) async {
    final call = CallEngine._(
      conversationId: conversationId,
      callId: callId,
      outgoing: true,
      userId: userId,
      repository: repository,
      mls: mls,
      onChange: onChange,
    );
    await call._start();
    return call;
  }

  /// Takes an incoming call. The invite may not have been read yet — see [_invite].
  static Future<CallEngine> incoming({
    required String conversationId,
    required String callId,
    required String userId,
    required PhemeRepository repository,
    required MlsService mls,
    required void Function(CallState) onChange,
  }) async {
    final call = CallEngine._(
      conversationId: conversationId,
      callId: callId,
      outgoing: false,
      userId: userId,
      repository: repository,
      mls: mls,
      onChange: onChange,
    );
    await call._start();
    return call;
  }

  CallState get state => CallState(
    callId: callId,
    conversationId: conversationId,
    status: _status,
    outgoing: outgoing,
    reason: _reason,
    muted: _muted,
    route: _route,
    seconds: _duration(),
    inviteReady: inviteReady,
  );

  int _duration() {
    final from = _connectedAt;
    if (from == null) return 0;
    final until = _status == CallStatus.ended
        ? (_endedAt ?? DateTime.now())
        : DateTime.now();
    final seconds = until.difference(from).inSeconds;
    return seconds < 0 ? 0 : seconds;
  }

  void _emit() => _onChange(state);

  void _setStatus(CallStatus status, [CallEndReason? reason]) {
    if (_status == CallStatus.ended) {
      return; // an ended call does not change its mind
    }
    _status = status;
    _reason = reason;

    // Darken the screen when the phone is at the ear for as long as the call is live, and let it go
    // when the call ends. Anchored to status so it covers every way in and out — answered, declined,
    // timed out, dropped. Best effort and fire-and-forget: it must never gate the call.
    if (status == CallStatus.ended) {
      unawaited(ProximityLock.release());
    } else {
      unawaited(ProximityLock.acquire());
    }

    _emit();
  }

  /// Common setup: hold the group still, pin the key, start reading the mailbox.
  ///
  /// The whole thing is guarded, and that guard is not defensive padding. The group freeze is a
  /// REFCOUNT held for the life of the call, and it is released in exactly one place: end(). But a
  /// call that fails to start never becomes a call — the engine is discarded before anyone can hold a
  /// reference to it, so nothing ever calls end() on it, so the refcount is never given back.
  ///
  /// And the freeze is not scoped to this conversation. It gates reconcileDevices for EVERY
  /// conversation in the session. So one failed call setup — the device is offline, it is not in the
  /// group yet, the catch-up times out — would permanently stop the app from admitting anybody's new
  /// device, anywhere, until it was restarted. Nothing would say so; chats would simply stop letting
  /// new devices in.
  Future<void> _start() async {
    // Hold the group's membership still for the duration. Admitting somebody's newly signed-in device
    // is a Commit, a Commit moves the MLS epoch, and the epoch is what our key is derived from — so
    // reconciling mid-call would pull the key out from under a conversation two people are having. It
    // can wait thirty seconds.
    final release = _mls.freezeGroupForCall();
    _releaseGroup = release;

    try {
      // Be at the group's CURRENT epoch before deriving anything, and only then hold it still.
      //
      // The exporter exports from the current epoch and no other. A device that is behind — the other
      // person's phone joined the group yesterday and this one has had no reason to notice — would seal
      // its invite under an epoch its peer has already moved past, and the peer cannot go back to it.
      // The invite arrives, cannot be opened, and is dropped without a sound: the phone buzzes from the
      // push, which no key protects, and then nothing rings.
      await _mls.catchUpToLatest(conversationId, _userId);

      await _deriveKeys();
    } on Object {
      // Give the freeze back before the engine is thrown away. Releasing is idempotent, so end() doing
      // it again later is harmless.
      release();
      _releaseGroup = null;
      rethrow;
    }

    _startPolling();

    _ringTimer = Timer(ringTimeout, () {
      if (_status == CallStatus.calling || _status == CallStatus.ringing) {
        unawaited(end(CallEndReason.unanswered, notifyPeer: true));
      }
    });

    // The duration is read from the clock, so something has to tell the UI the clock moved.
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (_status == CallStatus.connected) _emit();
    });
  }

  /// Derives this device's key ONCE, and remembers the epoch it came from.
  ///
  /// Cached in memory for the life of the call and never re-derived. The exporter is bound to the
  /// current MLS epoch, so a membership change moves it; a call that re-derived per signal would
  /// silently start sealing under a key its peer cannot open, mid-sentence. Pinning the bytes makes an
  /// epoch change during a call a non-event.
  Future<void> _deriveKeys() async {
    _identity = await _mls.myIdentity(_userId);
    final key = await _mls.callKeyFor(
      conversationId,
      _userId,
      callId,
      _identity,
    );
    if (key == null) {
      throw StateError(
        "this device is not in the conversation's encrypted group",
      );
    }
    _secret = key.secret;
    _epoch = key.epoch;
  }

  /// The key a given device seals with. Any member can derive any member's — that is how we read them.
  Future<Uint8List?> _peerSecret(String identity) async {
    final cached = _peerSecrets[identity];
    if (cached != null) return cached;

    final ExportedSecret? key = await _mls.callKeyFor(
      conversationId,
      _userId,
      callId,
      identity,
    );
    if (key == null) return null;

    _peerSecrets[identity] = key.secret;
    return key.secret;
  }

  CallHeader _header(int seq, {String? control}) => CallHeader(
    callId: callId,
    epoch: _epoch,
    from: _identity,
    seq: seq,
    control: control,
  );

  Future<void> _send(
    CallBody body, {
    bool ring = false,
    bool cancel = false,
  }) async {
    final secret = _secret;
    if (secret == null) return;

    try {
      final wire = await sealSignal(secret, _header(++_seq), body);
      await _repo.callSignal(
        conversationId,
        callId,
        wire,
        ring: ring,
        cancel: cancel,
      );
    } on Object {
      // A signal that does not go out is a call that does not connect, but there is nothing useful to
      // retry against here: the ring timeout is what gives up. Swallowing it keeps a failed hangup
      // from throwing out of a cleanup path.
    }
  }

  // --- the two halves of the exchange ------------------------------------------------------------

  /// Places the call: microphone, one complete offer, ring the other end.
  Future<void> invite() async {
    await _openMicrophone();
    final pc = await _peerConnection();

    final offer = await pc.createOffer({'offerToReceiveAudio': true});
    await pc.setLocalDescription(offer);
    await _gatheringComplete(pc);

    await _send(
      CallBody(kind: CallKind.invite, sdp: await _localSdp(pc)),
      ring: true,
    );
    _startReRinging();
  }

  /// Answers. Claims the call for THIS device first — every device the user is signed in on is
  /// ringing, and exactly one may pick up.
  ///
  /// The claim is a server-side lock and not a race, because by the time a device loses it, it has
  /// already opened the microphone. "Somebody else answered" cannot be delivered over a bus that is
  /// allowed to drop messages: a loser who never hears it keeps ringing with a live mic.
  ///
  /// Returns false when another of our devices won.
  Future<bool> answer(String deviceId) async {
    final won = await _repo.callAccept(conversationId, callId, deviceId);
    if (!won) {
      await end(CallEndReason.answeredElsewhere, notifyPeer: false);
      return false;
    }

    _setStatus(CallStatus.connecting);

    // The invite may still be in flight — on iOS the phone rings before we are allowed to do any of
    // the work that fetches it. Waiting is the whole reason [_invite] is a future.
    final String offerSdp;
    try {
      offerSdp = await _invite.future.timeout(const Duration(seconds: 10));
    } on TimeoutException {
      await end(CallEndReason.failed, notifyPeer: true);
      return false;
    }

    await _openMicrophone();
    final pc = await _peerConnection();

    await pc.setRemoteDescription(RTCSessionDescription(offerSdp, 'offer'));
    final answer = await pc.createAnswer({'offerToReceiveAudio': true});
    await pc.setLocalDescription(answer);
    await _gatheringComplete(pc);

    await _send(CallBody(kind: CallKind.answer, sdp: await _localSdp(pc)));
    return true;
  }

  /// Refuses an incoming call.
  Future<void> decline() async {
    await _send(const CallBody(kind: CallKind.decline));
    await end(CallEndReason.declined, notifyPeer: false);
  }

  /// Hangs up, from either end and at any point.
  Future<void> hangUp() async {
    // Hanging up on a call that never connected is a MISSED call, and it has to take the ring off the
    // other person's lock screen. Otherwise a notification sits there looking live and deep-links into
    // a call nobody is on.
    await _send(
      const CallBody(kind: CallKind.hangup),
      cancel: _status == CallStatus.calling,
    );
    await end(CallEndReason.hungUp, notifyPeer: false);
  }

  /// Keeps pointing the callee at the invite for as long as we are ringing.
  ///
  /// The invite is published exactly once, so a callee whose live stream is down at that moment —
  /// reconnecting, backgrounded, moving between cells — never hears it, and the call rings out against
  /// a phone that was sitting right there. The invite has not gone anywhere: it is in the mailbox for
  /// two minutes. Nothing was looking at it. So say it again, every few seconds.
  void _startReRinging() {
    _reRingTimer = Timer.periodic(reRingInterval, (_) {
      if (_status != CallStatus.calling) {
        _stopReRinging();
        return;
      }
      _repo.callRing(conversationId, callId).catchError((_) {
        // Best effort by construction: the next tick tries again, and the ring timeout gives up.
      });
    });
  }

  void _stopReRinging() {
    _reRingTimer?.cancel();
    _reRingTimer = null;
  }

  // --- reading the mailbox -----------------------------------------------------------------------

  /// The live stream only nudges; the signals are read from here.
  ///
  /// Polling as well as listening is not belt and braces — the bus is explicitly allowed to drop an
  /// event, and a dropped SDP answer is a call that silently never connects. Reading from a cursor
  /// makes a lost nudge cost a few hundred milliseconds instead of the call. It stops the moment the
  /// audio is up: there is nothing left to say.
  void _startPolling() {
    unawaited(_drain());
    _pollTimer = Timer.periodic(pollInterval, (_) {
      if (_status == CallStatus.connected || _status == CallStatus.ended) {
        _stopPolling();
        return;
      }
      unawaited(_drain());
    });
  }

  void _stopPolling() {
    _pollTimer?.cancel();
    _pollTimer = null;
  }

  /// Called when the live stream says this call has something new.
  void nudge() => unawaited(_drain());

  Future<void> _drain() async {
    if (_draining || _status == CallStatus.ended) return;
    _draining = true;
    try {
      final signals = await _repo.callSignals(
        conversationId,
        callId,
        since: _cursor,
      );
      for (final signal in signals) {
        if (signal.seq > _cursor) _cursor = signal.seq;
        await _handle(signal.ciphertext);
      }
    } on Object {
      // Transient. The next poll picks it up; the ring timeout eventually gives up.
    } finally {
      _draining = false;
    }
  }

  Future<void> _handle(Uint8List wire) async {
    final header = openHeader(wire);
    if (header == null) {
      return; // not a call signal; the server relays what it is handed
    }
    if (header.callId != callId) return;
    if (header.from == _identity) return; // our own signal, echoed back

    // Replay and reordering. The server could resend an old signal; a monotonic sequence per sender
    // means it gets nowhere.
    final last = _seen[header.from] ?? 0;
    if (header.seq <= last) return;
    _seen[header.from] = header.seq;

    if (header.control == epochMismatch) {
      await _onEpochMismatch(header);
      return;
    }

    // The sender derived their key at THEIR epoch. If we are not at the same one we cannot derive it —
    // the exporter only exports from the current epoch — so the two of us have to agree on an epoch
    // before we can say a word to each other.
    if (header.epoch != _epoch) {
      await _reconcileEpoch(header.epoch);
      if (header.epoch != _epoch) {
        return; // handled: we told them, or we gave up
      }
    }

    final secret = await _peerSecret(header.from);
    if (secret == null) return;

    final CallBody body;
    try {
      body = await openSignal(secret, wire);
    } on Object {
      // It did not open. Either it was tampered with (the header is bound in as additional data), or
      // it was sealed under a key we cannot derive. Neither is something to guess about.
      return;
    }
    await _onBody(body);
  }

  Future<void> _onBody(CallBody body) async {
    switch (body.kind) {
      case CallKind.invite:
        final sdp = body.sdp;
        if (sdp != null && !_invite.isCompleted) {
          _invite.complete(sdp);
          _emit(); // inviteReady moved: an incoming call can now actually be answered
        }

      case CallKind.answer:
        // The FIRST answer wins; every later one is ignored. Two of the callee's devices can pick up
        // in the same instant. The server's lock decides which, but a loser's answer may already be
        // in flight, and applying it would tear down the connection we just made.
        final pc = _pc;
        final sdp = body.sdp;
        if (pc == null || sdp == null) return;
        if (await pc.getRemoteDescription() != null) return;

        // That await is a gap the call can end across — the user hangs up, the ring times out. end()
        // closes the peer connection and nulls it, but the local `pc` above still points at the closed
        // one, and setting a description on it throws into a swallowed catch: the answer is lost with
        // no retry, and a real call hangs. Re-check before touching it.
        if (_status == CallStatus.ended || _pc != pc) return;

        _setStatus(CallStatus.connecting);
        await pc.setRemoteDescription(RTCSessionDescription(sdp, 'answer'));

      case CallKind.decline:
        await end(CallEndReason.declined, notifyPeer: false);

      case CallKind.busy:
        await end(CallEndReason.busy, notifyPeer: false);

      case CallKind.hangup:
        await end(CallEndReason.hungUp, notifyPeer: false);
    }
  }

  // --- epochs ------------------------------------------------------------------------------------

  /// The other end derived its key at an epoch we are not at, so neither of us can read the other.
  ///
  /// If we are BEHIND we can catch up — apply the Commits we missed, re-derive. If we are AHEAD we
  /// cannot: MLS's exporter will not export a past epoch, and there is no way back. So we say so, in
  /// the clear (there is nothing secret in "I am at epoch N"), and they re-derive and try again.
  Future<void> _reconcileEpoch(int theirs) async {
    if (theirs > _epoch) {
      await _mls.catchUpToEpoch(conversationId, _userId, theirs);
      await _deriveKeys();
      _peerSecrets.clear(); // every peer key was derived at the old epoch
      return;
    }

    // We are ahead. Tell them, once — a second identical complaint helps nobody.
    final wire = sealControl(_header(++_seq, control: epochMismatch));
    try {
      await _repo.callSignal(conversationId, callId, wire);
    } on Object {
      // Nothing to do about it.
    }
  }

  Future<void> _onEpochMismatch(CallHeader header) async {
    if (header.epoch <= _epoch) return; // already there

    await _mls.catchUpToEpoch(conversationId, _userId, header.epoch);
    final before = _epoch;
    await _deriveKeys();
    _peerSecrets.clear();

    if (_epoch == before) {
      // We could not get to where they are. Rather than ring forever under a key nobody can open, say
      // plainly that this device is out of step.
      await end(CallEndReason.outOfSync, notifyPeer: true);
      return;
    }

    // Re-offer under the new key, if we are the one placing the call.
    final pc = _pc;
    if (outgoing && pc != null) {
      final sdp = await _localSdp(pc);
      if (sdp.isNotEmpty) {
        await _send(CallBody(kind: CallKind.invite, sdp: sdp));
      }
    }
  }

  // --- controls ----------------------------------------------------------------------------------

  /// Mutes by disabling the track rather than stopping it.
  ///
  /// A stopped track cannot be restarted — it would need a fresh getUserMedia and a renegotiation to
  /// put back — and it drops the OS recording indicator, which would tell the other person you had
  /// hung up when you had only stopped talking. A disabled track stays in the peer connection and
  /// transmits silence, which is what mute means.
  void setMuted(bool muted) {
    _muted = muted;
    for (final track
        in _localStream?.getAudioTracks() ?? const <MediaStreamTrack>[]) {
      track.enabled = !muted;
    }
    _emit();
  }

  /// Moves the call between the earpiece, the loudspeaker and a headset.
  ///
  /// The mobile answer to the web's setSinkId, which does not exist on iOS at all. Here it is a ROUTE
  /// rather than a device: a phone has an earpiece and a speaker, not a list of sound cards, and the
  /// OS owns the choice between them.
  Future<void> setRoute(AudioRoute route) async {
    _route = route;
    switch (route) {
      case AudioRoute.speaker:
        await Helper.setSpeakerphoneOn(true);
      case AudioRoute.earpiece:
        await Helper.setSpeakerphoneOn(false);
      case AudioRoute.bluetooth:
        await Helper.setSpeakerphoneOnButPreferBluetooth();
    }
    _emit();
  }

  // --- WebRTC ------------------------------------------------------------------------------------

  Future<void> _openMicrophone() async {
    if (_localStream != null) return;
    _localStream = await navigator.mediaDevices.getUserMedia({
      'audio': true,
      'video': false,
    });
    // A mic opened after the user already pressed mute must not come up live.
    for (final track in _localStream!.getAudioTracks()) {
      track.enabled = !_muted;
    }
  }

  Future<RTCPeerConnection> _peerConnection() async {
    final existing = _pc;
    if (existing != null) return existing;

    final servers = await _repo.iceServers(conversationId: conversationId);
    final pc = await createPeerConnection({
      'iceServers': servers.map((s) => s.toJson()).toList(),
    });
    _pc = pc;

    final local = _localStream;
    if (local != null) {
      for (final track in local.getTracks()) {
        await pc.addTrack(track, local);
      }
    }

    // The other person's voice. flutter_webrtc plays a remote audio track automatically once it is
    // attached, so there is nothing to build here — unlike the web, which needs an <audio> element
    // that survives navigation.
    pc.onTrack = (event) {
      if (event.streams.isNotEmpty) _remoteStream = event.streams.first;
    };

    pc.onConnectionState = (state) {
      switch (state) {
        case RTCPeerConnectionState.RTCPeerConnectionStateConnected:
          _connectedAt ??= DateTime.now();
          _setStatus(CallStatus.connected);
          _stopPolling();
          _stopReRinging();
          _ringTimer?.cancel();
          _ringTimer = null;

        case RTCPeerConnectionState.RTCPeerConnectionStateFailed:
          unawaited(end(CallEndReason.failed, notifyPeer: true));

        default:
        // `disconnected` recovers on its own; only `failed` and an explicit hangup end a call.
        // Ending here would drop a call that was about to come back.
      }
    };

    return pc;
  }

  /// The local SDP AFTER gathering.
  ///
  /// Re-read from the peer connection rather than taken from what createOffer returned, because that
  /// description carries NO candidates — they are added as they are gathered. Using it would send an
  /// offer nobody can connect to.
  Future<String> _localSdp(RTCPeerConnection pc) async =>
      (await pc.getLocalDescription())?.sdp ?? '';

  /// Waits for ICE gathering, but not forever.
  ///
  /// A TURN allocation can be slow, and a candidate that has not arrived by now is one we can live
  /// without — the ones that matter (host, and the reflexive address from STUN) are there in well
  /// under a second.
  Future<void> _gatheringComplete(RTCPeerConnection pc) async {
    if (pc.iceGatheringState ==
        RTCIceGatheringState.RTCIceGatheringStateComplete) {
      return;
    }

    final done = Completer<void>();
    pc.onIceGatheringState = (state) {
      if (state == RTCIceGatheringState.RTCIceGatheringStateComplete &&
          !done.isCompleted) {
        done.complete();
      }
    };

    await done.future.timeout(iceGatherTimeout, onTimeout: () {});
  }

  // --- teardown ----------------------------------------------------------------------------------

  /// Ends the call and releases everything, exactly once.
  ///
  /// The microphone is the part that must not be got wrong: a call that ends without stopping its
  /// tracks leaves the OS recording indicator on and the mic live. EVERY path out of a call goes
  /// through here — which is also why the group freeze is released here and nowhere else.
  Future<void> end(CallEndReason reason, {required bool notifyPeer}) async {
    if (_status == CallStatus.ended) return;

    _endedAt =
        DateTime.now(); // before setStatus: the state it emits reports the duration
    _setStatus(CallStatus.ended, reason);

    // A call that gives up while it was still ringing must also close the notification it put on the
    // other person's lock screen.
    if (notifyPeer) {
      await _send(
        const CallBody(kind: CallKind.hangup),
        cancel: reason == CallEndReason.unanswered,
      );
    }

    _stopPolling();
    _stopReRinging();
    _ringTimer?.cancel();
    _ringTimer = null;
    _tick?.cancel();
    _tick = null;

    for (final track
        in _localStream?.getTracks() ?? const <MediaStreamTrack>[]) {
      await track.stop();
    }
    await _localStream?.dispose();
    _localStream = null;

    await _remoteStream?.dispose();
    _remoteStream = null;

    await _pc?.close();
    _pc = null;

    _releaseGroup?.call();
    _releaseGroup = null;
  }
}

/// True when the error means the server has calling switched off.
bool isCallingDisabled(Object e) =>
    e is CallingUnavailableException ||
    (e is ApiException && e.statusCode == 503);
