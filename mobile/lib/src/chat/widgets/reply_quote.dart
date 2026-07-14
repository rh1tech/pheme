// The quoted message above a reply.
//
// The quote is rendered from the message THIS DEVICE ALREADY HOLDS, never from text copied into the
// reply. That is a security property, not a storage optimisation: if the sender could put the quoted
// text in the message, they could quote you as having said anything at all, and the recipient would
// have no way to check. So the reply carries an id and nothing else, and each device looks the
// original up for itself.
//
// Which means a device sometimes cannot show the quote — the quoted message was sent before it joined
// the group, so it can never decrypt it. It says so, rather than showing a plausible-looking blank.

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';

class ReplyQuote extends StatelessWidget {
  const ReplyQuote({
    super.key,
    required this.author,
    required this.text,
    this.onTap,
    this.compact = false,
  });

  /// Who wrote the quoted message. Null when we cannot read it.
  final String? author;

  /// The quoted text, or null when this device cannot read the original.
  final String? text;

  /// Jumps to the quoted message.
  final VoidCallback? onTap;

  /// The composer variant, which is a shade tighter.
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final accent = theme.colorScheme.primary;

    final unreadable = text == null;

    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        margin: EdgeInsets.only(bottom: compact ? 0 : 6),
        padding: const EdgeInsets.only(left: 8),
        decoration: BoxDecoration(
          border: Border(left: BorderSide(color: accent, width: 3)),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              author ?? l10n.t('chat.replyUnknown'),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: theme.textTheme.labelMedium?.copyWith(
                color: accent,
                fontWeight: FontWeight.w600,
              ),
            ),
            Text(
              // Not an ellipsis, and not a blank: this device will NEVER be able to read that message,
              // and implying that it is still loading would be a lie it never resolves.
              text ?? l10n.t('chat.replyUnavailable'),
              maxLines: compact ? 1 : 2,
              overflow: TextOverflow.ellipsis,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
                fontStyle: unreadable ? FontStyle.italic : null,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
