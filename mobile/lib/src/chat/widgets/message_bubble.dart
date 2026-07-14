// A chat message.
//
// The bubble's shape carries the whole own/other distinction, exactly as on the web: there is no
// drawn tail and no avatar. What says "this is mine" is the alignment, the iris tint, and WHICH
// BOTTOM CORNER is nearly square (2px) instead of rounded (16px) — bottom-left for the other person,
// bottom-right for you. Cheap to draw, and it reads instantly.

import 'package:flutter/cupertino.dart' show cupertinoTextSelectionControls;
import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../widgets/adaptive/platform.dart';
import '../chat_time.dart';

/// 16px — the Mantine `lg` radius the web bubble uses.
const _round = Radius.circular(16);

/// 2px — the Mantine `xs` radius. This is the "tail" corner.
const _tail = Radius.circular(2);

class MessageBubble extends StatelessWidget {
  const MessageBubble({
    super.key,
    required this.body,
    required this.createdAt,
    required this.isOwn,
    this.senderName,
  });

  /// The decrypted body, or null when this device cannot read the message.
  final String? body;
  final String createdAt;
  final bool isOwn;

  /// Shown above other people's messages in a group. Null in a direct chat — there is only one other
  /// person and their name is in the header.
  final String? senderName;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final dark = theme.brightness == Brightness.dark;

    final background = isOwn
        ? (dark ? const Color(0x38A888F5) : const Color(0x1F7740EE))
        : (dark ? const Color(0xFF1F2126) : Colors.white);

    return Align(
      alignment: isOwn ? Alignment.centerRight : Alignment.centerLeft,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 544), // --pheme-bubble-max
        margin: const EdgeInsets.symmetric(vertical: 3),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: background,
          borderRadius: BorderRadius.only(
            topLeft: _round,
            topRight: _round,
            bottomLeft: isOwn ? _round : _tail,
            bottomRight: isOwn ? _tail : _round,
          ),
          boxShadow: dark
              ? null
              : const [
                  BoxShadow(
                    color: Color(0x0F141028),
                    blurRadius: 2,
                    offset: Offset(0, 1),
                  ),
                ],
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            if (senderName != null && !isOwn)
              Padding(
                padding: const EdgeInsets.only(bottom: 2),
                child: Text(
                  senderName!,
                  style: theme.textTheme.labelMedium?.copyWith(
                    color: dark ? const Color(0xFFA888F5) : kIris,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            _Body(body: body, l10n: l10n),
            const SizedBox(height: 2),
            Align(
              alignment: Alignment.centerRight,
              child: Text(
                bubbleTime(l10n, createdAt),
                style: theme.textTheme.bodySmall?.copyWith(
                  fontSize: 11,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.body, required this.l10n});

  final String? body;
  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final text = body;

    // Not a loading state, and it must never look like one. MLS gives a device no access to what was
    // said before it joined, and a message decrypts exactly once — so this is permanent, and saying
    // so plainly beats an ellipsis that implies it is about to resolve.
    if (text == null) {
      return Text(
        l10n.t('chat.notAvailableOnThisDevice'),
        style: theme.textTheme.bodyMedium?.copyWith(
          fontStyle: FontStyle.italic,
          color: theme.colorScheme.onSurfaceVariant,
        ),
      );
    }

    return SelectableText(
      text,
      style: theme.textTheme.bodyMedium,
      // iOS gets the Cupertino selection handles and toolbar; Android the Material ones.
      selectionControls: isCupertino(context)
          ? cupertinoTextSelectionControls
          : materialTextSelectionControls,
    );
  }
}
