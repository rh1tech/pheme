// What a call looks like from the outside.

enum CallStatus {
  /// Placing it: the other end has not picked up.
  calling,

  /// Somebody is calling us.
  ringing,

  /// Answered; the peers are finding each other.
  connecting,

  /// Audio is flowing.
  connected,

  ended,
}

/// Why a call ended. The UI says a different thing for each of these, and so it should — "no answer"
/// and "they are on another call" and "this device is out of step" are three different situations, and
/// collapsing them into "call failed" tells the user nothing they can act on.
enum CallEndReason {
  hungUp,
  declined,
  busy,
  unanswered,
  answeredElsewhere,
  failed,
  outOfSync;

  /// The l10n key for what to tell the user.
  String get messageKey => switch (this) {
    CallEndReason.hungUp => 'call.endedHungUp',
    CallEndReason.declined => 'call.endedDeclined',
    CallEndReason.busy => 'call.endedBusy',
    CallEndReason.unanswered => 'call.endedUnanswered',
    CallEndReason.answeredElsewhere => 'call.endedAnsweredElsewhere',
    CallEndReason.failed => 'call.endedFailed',
    CallEndReason.outOfSync => 'call.endedOutOfSync',
  };

  /// What the caller writes into the transcript, or null when the call leaves no record.
  ///
  /// Only an unanswered outgoing call leaves one, and only the caller writes it — so exactly one
  /// message is posted, by the one device that knows the call rang out.
  String? get callEventOutcome => switch (this) {
    CallEndReason.unanswered => 'missed',
    CallEndReason.declined => 'declined',
    CallEndReason.failed || CallEndReason.outOfSync => 'failed',
    _ => null,
  };
}

/// Where the call's audio is going.
///
/// A route, not a device. The web picks an output device with setSinkId; a phone has an earpiece, a
/// loudspeaker and whatever is paired, and the OS owns the choice between them. (setSinkId does not
/// exist on iOS at all, which is why the web hides the control there entirely.)
enum AudioRoute { earpiece, speaker, bluetooth }

class CallState {
  const CallState({
    required this.callId,
    required this.conversationId,
    required this.status,
    required this.outgoing,
    required this.muted,
    required this.route,
    required this.seconds,
    required this.inviteReady,
    this.reason,
  });

  final String callId;
  final String conversationId;
  final CallStatus status;

  /// True when we placed it.
  final bool outgoing;

  final CallEndReason? reason;

  /// True while the microphone is not being sent.
  final bool muted;

  final AudioRoute route;

  /// Seconds of connected audio, for the record the call leaves in the chat.
  final int seconds;

  /// Whether the invite's SDP is in hand.
  ///
  /// Has no counterpart on the web, and exists because of CallKit: iOS insists a call be reported the
  /// instant its push arrives, and only then may the app go and fetch the invite, catch up the MLS
  /// epoch and open the envelope. So the phone can be ringing before there is anything to answer WITH,
  /// and the answer button has to wait rather than fail.
  final bool inviteReady;

  bool get isActive => status != CallStatus.ended;
  bool get isIncomingRing => status == CallStatus.ringing && !outgoing;

  /// The l10n key for the status line.
  String get statusKey => switch (status) {
    CallStatus.calling => 'call.statusCalling',
    CallStatus.ringing => 'call.statusRinging',
    CallStatus.connecting => 'call.statusConnecting',
    CallStatus.connected => 'call.statusConnected',
    CallStatus.ended => reason?.messageKey ?? 'call.statusEnded',
  };

  /// mm:ss, for a connected call.
  String get elapsed {
    final minutes = (seconds ~/ 60).toString().padLeft(2, '0');
    final secs = (seconds % 60).toString().padLeft(2, '0');
    return '$minutes:$secs';
  }
}
