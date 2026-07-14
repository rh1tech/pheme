// The one place a call is held.
//
// Deliberately NOT autoDispose, and deliberately top-level. A call outlives the screen it was placed
// from: you can navigate back to the conversation list, or into another chat, and still be talking. A
// provider scoped to a widget would tear the call down the moment that widget left the tree, and the
// microphone would go with it.
//
// It is also the ONLY place that talks to the platform's call UI. Funnelling that here is what stops
// "the call screen is stuck" from becoming a game of whack-a-mole across a dozen forgotten teardown
// paths: status == ended implies endCall, in one line, in one place.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/chat_providers.dart';
import '../core/providers.dart';
import '../crypto/mls_device.dart';
import '../data/app_providers.dart';
import 'call_engine.dart';
import 'call_state.dart';

class CallController extends Notifier<CallState?> {
  CallEngine? _engine;

  @override
  CallState? build() {
    // The live stream nudges us that a call has moved. It is ONLY a nudge — the signal itself is read
    // from the server's mailbox, because the bus is allowed to drop events and a dropped SDP answer is
    // a call that silently never connects.
    ref.listen(liveEventsProvider, (_, next) {
      final signal = next.value?.callSignal;
      if (signal == null) return;

      final engine = _engine;
      final myUserId = ref.read(myUserIdProvider);

      // Our own other device placed this call. It must not ring back at us.
      if (signal.fromUserId == myUserId) return;

      if (engine != null && engine.callId == signal.callId) {
        engine.nudge();
        return;
      }

      // A call we know nothing about. Somebody is calling.
      final conversationId = next.value?.conversationId;
      if (conversationId != null && engine == null) {
        unawaited(_receive(conversationId, signal.callId));
      }
    });

    ref.onDispose(
      () => unawaited(_engine?.end(CallEndReason.hungUp, notifyPeer: true)),
    );
    return null;
  }

  /// Places a call.
  Future<void> place(String conversationId) async {
    if (_engine != null) return; // already on one

    final engine = await CallEngine.place(
      conversationId: conversationId,
      // A UUID, because iOS identifies a call to CallKit BY a UUID and will not report one under
      // anything else. Opaque to the server, so the shape is free.
      callId: newUuid(),
      userId: ref.read(myUserIdProvider),
      repository: ref.read(repositoryProvider),
      mls: ref.read(mlsServiceProvider),
      onChange: _onChange,
    );
    _engine = engine;
    state = engine.state;

    await engine.invite();
  }

  /// Takes an incoming call: start the engine, and let it read the invite out of the mailbox.
  Future<void> _receive(String conversationId, String callId) async {
    if (_engine != null) {
      // Already on a call. The polite answer is "busy", but we cannot send one — a busy signal has to
      // be sealed under a key derived for THAT call, and deriving it would mean standing up a whole
      // second engine. The caller's ring timeout says the same thing thirty seconds later.
      return;
    }

    try {
      final engine = await CallEngine.incoming(
        conversationId: conversationId,
        callId: callId,
        userId: ref.read(myUserIdProvider),
        repository: ref.read(repositoryProvider),
        mls: ref.read(mlsServiceProvider),
        onChange: _onChange,
      );
      _engine = engine;
      state = engine.state;
    } on Object {
      // We could not set up to take the call — this device is not in the group, or the key would not
      // derive. Ringing a phone for a call it cannot possibly answer is worse than staying quiet.
      _engine = null;
      state = null;
    }
  }

  Future<void> answer() async {
    final engine = _engine;
    if (engine == null) return;

    // The PUSH device id, issued by the server — NOT the MLS one. The answer lock is keyed on it, and
    // conflating the two is a bug this codebase has already had.
    //
    // ensureRegistered rather than a plain read, because a device can be signed in and ringing without
    // ever having registered — the user declined notifications, or this is a Mac with no Firebase. It
    // deliberately does not prompt: being asked for notification permission by a phone that is already
    // ringing would be absurd, and declining would silently make the call unanswerable.
    final deviceId = await ref
        .read(deviceControllerProvider.notifier)
        .ensureRegistered();

    if (deviceId == null) {
      // Without an id there is no way to claim the call, and answering anyway would leave every one of
      // the user's devices believing it had won.
      await engine.end(CallEndReason.failed, notifyPeer: true);
      return;
    }

    await engine.answer(deviceId);
  }

  Future<void> decline() => _engine?.decline() ?? Future.value();

  Future<void> hangUp() => _engine?.hangUp() ?? Future.value();

  void setMuted(bool muted) => _engine?.setMuted(muted);

  Future<void> setRoute(AudioRoute route) =>
      _engine?.setRoute(route) ?? Future.value();

  /// Clears an ended call off the screen.
  void dismiss() {
    if (_engine?.state.status != CallStatus.ended) return;
    _engine = null;
    state = null;
  }

  void _onChange(CallState next) {
    state = next;
    if (next.status != CallStatus.ended) return;

    // The call is over. Two things follow, and both belong here rather than scattered through the
    // engine's exit paths.
    unawaited(_recordCallEvent(next));

    // Leave the ended state on screen for a moment so the user can read "Declined" or "No answer",
    // then clear it.
    Timer(const Duration(seconds: 2), () {
      if (_engine?.callId == next.callId) dismiss();
    });
  }

  /// Writes the missed/declined/failed call into the transcript.
  ///
  /// Only the CALLER writes it, and only for a call that never connected — so exactly one message is
  /// posted, by the one device that knows the call rang out. It is a real encrypted message: the other
  /// end reads it from its own history, on every device, forever.
  Future<void> _recordCallEvent(CallState call) async {
    if (!call.outgoing || call.seconds > 0) return;

    final outcome = call.reason?.callEventOutcome;
    if (outcome == null) return;

    try {
      await ref
          .read(mlsServiceProvider)
          .postCallEvent(
            call.conversationId,
            ref.read(myUserIdProvider),
            outcome,
          );
    } on Object {
      // A record we could not write is not worth failing a teardown over.
    }
  }
}

final callProvider = NotifierProvider<CallController, CallState?>(
  CallController.new,
);
