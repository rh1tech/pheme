// A call that was never answered, rendered in the transcript.
//
// It is a real encrypted message, not a UI flourish — the caller posts it, the other end reads it
// from its own history on every device, forever. But it is a system aside rather than speech, so it
// is centred and pill-shaped rather than a bubble on either side.

import 'dart:convert';

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../chat_time.dart';

/// What became of the call. The body of the encrypted message is `{"outcome":"..."}`.
enum CallOutcome {
  missed,
  declined,
  failed;

  static CallOutcome? parse(String? body) {
    if (body == null) return null;
    try {
      final outcome = (jsonDecode(body) as Map<String, dynamic>)['outcome'];
      return switch (outcome) {
        'missed' => CallOutcome.missed,
        'declined' => CallOutcome.declined,
        'failed' => CallOutcome.failed,
        _ => null,
      };
    } on FormatException {
      return null;
    }
  }
}

/// What the transcript calls this outcome.
///
/// Pulled out of the bubble because the conversation LIST needs the same sentence: its preview was
/// showing the message's raw body, so a failed call appeared in the chat list as
/// `{"outcome":"failed"}`. The body of a call event is JSON by design — it has to survive being read
/// by a client that does not know this event type — and JSON is not something to show anybody.
String callEventLabel(
  CallOutcome outcome, {
  required bool isOwn,
  required AppLocalizations l10n,
}) => switch (outcome) {
  CallOutcome.declined => l10n.t(
    isOwn ? 'call.eventDeclinedOut' : 'call.eventDeclinedIn',
  ),
  CallOutcome.failed => l10n.t('call.eventFailed'),
  // The same unanswered call is "No answer" to the person who made it and "Missed call" to the
  // person who did not hear it.
  CallOutcome.missed => l10n.t(
    isOwn ? 'call.eventMissedOut' : 'call.eventMissedIn',
  ),
};

class CallEventBubble extends StatelessWidget {
  const CallEventBubble({
    super.key,
    required this.outcome,
    required this.createdAt,
    required this.isOwn,
  });

  final CallOutcome outcome;
  final String createdAt;

  /// Whether WE placed the call. It changes what the event means: the same unanswered call is "No
  /// answer" to the person who made it and "Missed call" to the person who did not hear it.
  final bool isOwn;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final dark = theme.brightness == Brightness.dark;

    final missed = outcome == CallOutcome.missed && !isOwn;
    final color = missed
        ? (dark ? const Color(0xFFFF8787) : const Color(0xFFC92A2A))
        : theme.colorScheme.onSurfaceVariant;

    final label = callEventLabel(outcome, isOwn: isOwn, l10n: l10n);

    return Center(
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(999),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              outcome == CallOutcome.declined
                  ? Icons.phone_disabled_outlined
                  : Icons.phone_missed_outlined,
              size: 15,
              color: color,
            ),
            const SizedBox(width: 6),
            Text(
              label,
              style: theme.textTheme.bodySmall?.copyWith(
                color: color,
                fontWeight: FontWeight.w500,
              ),
            ),
            const SizedBox(width: 6),
            Text(
              bubbleTime(l10n, createdAt),
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The pill that pins above each day's messages.
class DateSeparator extends StatelessWidget {
  const DateSeparator({super.key, required this.day});

  final DateTime day;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Center(
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 8),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 2),
        decoration: BoxDecoration(
          color: Colors.black.withValues(alpha: 0.65),
          borderRadius: BorderRadius.circular(999),
        ),
        child: Text(
          dayLabel(l10n, day),
          style: const TextStyle(
            color: Colors.white,
            fontSize: 12,
            fontWeight: FontWeight.w500,
          ),
        ),
      ),
    );
  }
}
