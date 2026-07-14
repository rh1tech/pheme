// The bridge between the platform's ringer and the call engine.
//
// Constructed in main(), OUTSIDE the widget tree, and that is not a style choice: a call has to ring
// when the app was not running at all — cold-launched in the background by a push, with no route
// mounted and nothing on screen. A widget cannot do that, because there is no widget yet.
//
// ---------------------------------------------------------------------------------------------
// THE RULE THAT SHAPES EVERYTHING HERE: iOS requires that EVERY PushKit VoIP push report an incoming
// call to CallKit, immediately and synchronously. Not most of them. Every one. A push that does not
// gets the app killed on the spot, and after a few offences iOS stops delivering VoIP pushes to it at
// all — permanently, for that install.
//
// But the invite is NOT in the push. The push carries {callId, conversationId, callerName} and
// nothing else; the SDP is in the server's mailbox, sealed under a key derived from the conversation's
// MLS group. Fetching it means network, secure storage, and an MLS catch-up — none of which can happen
// before the call is reported.
//
// So the order is forced, and it is inside out:
//
//   1. the push arrives     -> report the call to CallKit. Ring. (Native, in AppDelegate.swift.)
//   2. arm a WATCHDOG       -> because everything after this can fail, and a CallKit call that is
//                              never ended is a call screen the user cannot get rid of.
//   3. the engine starts    -> read the mailbox, catch up the epoch, derive the key, open the invite.
//   4. the user answers     -> claim the call, wait for the invite if it is still in flight, answer.
//
// The phone is therefore ringing before there is anything to answer WITH. That is what CallState's
// inviteReady is for, and why answering waits rather than fails.
// ---------------------------------------------------------------------------------------------

import 'dart:async';

import 'package:flutter_callkit_incoming/entities/entities.dart';
import 'package:flutter_callkit_incoming/flutter_callkit_incoming.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'call_controller.dart';
import 'call_state.dart';

/// How long a reported call may go without its invite before we give up on it.
///
/// Everything between the ring and the invite can fail — the device is offline, the mailbox entry has
/// expired (they live two minutes), the MLS catch-up needs a Commit the server no longer has, this
/// device is not in the group. None of those produce an error the user could act on, and all of them
/// leave a call screen ringing at a call that will never connect. Ten seconds, then end it honestly.
const _inviteDeadline = Duration(seconds: 10);

class CallService {
  CallService(this._ref);

  final Ref _ref;

  StreamSubscription<CallEvent?>? _events;
  Timer? _watchdog;

  /// Calls we have already ended. A late or duplicate push for one of these must be reported and then
  /// immediately ended — never rung.
  final _ended = <String>{};

  void start() {
    _events = FlutterCallkitIncoming.onEvent.listen(_onPlatformEvent);

    // The engine is the source of truth about the call; the platform ringer is a view of it. When a
    // call ends for ANY reason — declined, hung up, the peer gave up, the watchdog fired — the ringer
    // has to be told, in one place. This listener is that place, and it is why "the call screen is
    // stuck" cannot become a game of whack-a-mole across a dozen teardown paths.
    _ref.listen<CallState?>(callProvider, (previous, next) {
      if (next == null) return;

      if (next.status == CallStatus.ended) {
        _ended.add(next.callId);
        _watchdog?.cancel();
        _watchdog = null;
        unawaited(FlutterCallkitIncoming.endCall(next.callId));
        return;
      }

      if (next.status == CallStatus.connected) {
        // Stops the platform's own "connecting" timer and starts its call duration.
        unawaited(FlutterCallkitIncoming.setCallConnected(next.callId));
      }

      // The invite landed, so the deadline has been met.
      if (next.inviteReady) {
        _watchdog?.cancel();
        _watchdog = null;
      }
    });
  }

  Future<void> dispose() async {
    await _events?.cancel();
    _watchdog?.cancel();
  }

  /// Shows the platform ringer for an incoming call.
  ///
  /// On iOS the native side has ALREADY reported the call by the time this runs — it has to have, or
  /// the app would be dead. This is the Android path and the belt-and-braces iOS one; CallKit ignores
  /// a second report of a call it already knows about.
  Future<void> ring({
    required String callId,
    required String conversationId,
    required String callerName,
  }) async {
    // A push for a call we have already ended — a duplicate, or one that arrived late. Report it and
    // end it at once: the report is mandatory, the ring is not.
    if (_ended.contains(callId)) {
      await FlutterCallkitIncoming.showCallkitIncoming(
        _params(callId, conversationId, callerName),
      );
      await FlutterCallkitIncoming.endCall(callId);
      return;
    }

    await FlutterCallkitIncoming.showCallkitIncoming(
      _params(callId, conversationId, callerName),
    );
    _armWatchdog(callId);
  }

  /// Takes a ringing call off the screen. The caller hung up before we answered.
  Future<void> cancelRing(String callId) async {
    _ended.add(callId);
    _watchdog?.cancel();
    _watchdog = null;
    await FlutterCallkitIncoming.endCall(callId);
  }

  /// Ends a call that rang but never produced an invite.
  ///
  /// Without this the user is left staring at a call screen that will not connect and will not go
  /// away, and on iOS a CallKit call that is never ended keeps the app alive in the background
  /// indefinitely. It is not a nicety.
  void _armWatchdog(String callId) {
    _watchdog?.cancel();
    _watchdog = Timer(_inviteDeadline, () {
      final call = _ref.read(callProvider);
      if (call?.callId != callId) return;
      if (call!.inviteReady) return;

      unawaited(_ref.read(callProvider.notifier).hangUp());
      unawaited(FlutterCallkitIncoming.endCall(callId));
      _ended.add(callId);
    });
  }

  Future<void> _onPlatformEvent(CallEvent? event) async {
    if (event == null) return;
    final notifier = _ref.read(callProvider.notifier);

    switch (event) {
      // The user picked up — from the lock screen, the CallKit screen, or the notification. The engine
      // claims the call server-side (every device the user is signed in on is ringing and exactly one
      // may win) and then waits for the invite, which on iOS may still be in flight.
      case CallEventActionCallAccept():
        await notifier.answer();

      case CallEventActionCallDecline():
        await notifier.decline();

      // The platform ended it: swiped away, or its own timeout fired. The engine may already be gone,
      // in which case this is a no-op — which is exactly what we want, because the alternative is
      // caring about which of the two got there first.
      case CallEventActionCallEnded(:final callKitParams):
        _ended.add(callKitParams.id);
        await notifier.hangUp();

      case CallEventActionCallTimeout(:final id):
        _ended.add(id);
        await notifier.hangUp();

      // Muting from the system call UI, which is a different control from ours and has to drive the
      // same state — otherwise the two disagree about whether the mic is live.
      case CallEventActionCallToggleMute(:final isMuted):
        notifier.setMuted(isMuted);

      // iOS: CallKit has activated the audio session.
      //
      // CallKit OWNS the AVAudioSession, and flutter_webrtc opens the microphone through it. Opening
      // it before this fires is the classic cause of a call that connects and then carries no audio in
      // either direction — most reliably on the first call after a cold start, which is the one a user
      // is most likely to try. There is nothing to do here (flutter_webrtc handles the session itself
      // once CallKit has activated it); the case exists so that this is written down somewhere rather
      // than rediscovered.
      case CallEventActionCallToggleAudioSession():
        break;

      case CallEventActionDidUpdateDevicePushTokenVoip():
      case CallEventActionCallIncoming():
      case CallEventActionCallStart():
      case CallEventActionCallConnected():
      case CallEventActionCallCallback():
      case CallEventActionCallToggleHold():
      case CallEventActionCallToggleDmtf():
      case CallEventActionCallToggleGroup():
      case CallEventActionCallCustom():
        break;
    }
  }

  CallKitParams _params(
    String callId,
    String conversationId,
    String callerName,
  ) {
    return CallKitParams(
      id: callId,
      nameCaller: callerName,
      appName: 'Pheme',
      handle: conversationId,
      type: 0, // audio
      duration:
          35000, // matches the caller's ring timeout, so both ends give up together
      extra: {'conversationId': conversationId},
      android: const AndroidParams(
        isCustomNotification: true,
        isShowLogo: false,
        // The lock-screen ringer. NOT ConnectionService: that would buy the system in-call UI at the
        // cost of MANAGE_OWN_CALLS, a PhoneAccount the user has to enable by hand on several OEMs, and
        // a long tail of vendor breakage. A full-screen intent gives us the one thing we actually
        // need, which is to ring a locked phone.
        isShowFullLockedScreen: true,
        ringtonePath: 'system_ringtone_default',
      ),
      ios: const IOSParams(
        supportsVideo: false,
        maximumCallGroups: 1,
        maximumCallsPerCallGroup: 1,
        audioSessionMode: 'voiceChat',
        supportsDTMF: false,
        supportsHolding: false,
        supportsGrouping: false,
        supportsUngrouping: false,
        ringtonePath: 'system_ringtone_default',
      ),
    );
  }
}

final callServiceProvider = Provider<CallService>((ref) {
  final service = CallService(ref);
  ref.onDispose(service.dispose);
  return service;
});
