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
    this.startsRun = true,
    this.endsRun = true,
    this.onLongPress,
    this.quote,
    this.photos,
  });

  /// The decrypted body, or null when this device cannot read the message.
  final String? body;
  final String createdAt;
  final bool isOwn;

  /// The quoted message this one replies to, if any.
  final Widget? quote;

  /// The photos this message carries, if any.
  final Widget? photos;

  /// Shown above other people's messages in a group. Null in a direct chat — there is only one other
  /// person and their name is in the header.
  final String? senderName;

  /// Whether this message begins a run from the same sender, and whether it ends one.
  ///
  /// A RUN is consecutive messages from one person, close together in time — and treating it as one
  /// visual block is the thing that makes a chat read like a conversation rather than a list. Telegram
  /// does it; the web client here does not (every message is a full standalone bubble), and on a phone
  /// that reads as shouty and wastes a lot of vertical space, which is scarce.
  ///
  /// So: the name appears once, at the top of the run. The time appears once, at the bottom. And only
  /// the LAST bubble in the run gets the squared-off tail corner, which is what makes the run look like
  /// one utterance with a single tail rather than a stack of identical blocks.
  final bool startsRun;
  final bool endsRun;

  final VoidCallback? onLongPress;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final dark = theme.brightness == Brightness.dark;

    final background = isOwn
        ? (dark ? const Color(0x38A888F5) : const Color(0x1F7740EE))
        : (dark ? const Color(0xFF1F2126) : Colors.white);

    // The tail corner belongs to the last bubble of the run. Mid-run bubbles keep it rounded, so the
    // run reads as one block.
    final tailCorner = endsRun ? _tail : _round;

    return Align(
      alignment: isOwn ? Alignment.centerRight : Alignment.centerLeft,
      child: GestureDetector(
        onLongPress: onLongPress,
        child: Container(
          constraints: const BoxConstraints(
            maxWidth: 544,
          ), // --pheme-bubble-max
          // Tight inside a run, loose between runs. This spacing IS the grouping.
          margin: EdgeInsets.only(
            top: startsRun ? 6 : 1,
            bottom: endsRun ? 6 : 1,
          ),
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            color: background,
            borderRadius: BorderRadius.only(
              topLeft: _round,
              topRight: _round,
              bottomLeft: isOwn ? _round : tailCorner,
              bottomRight: isOwn ? tailCorner : _round,
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
              // Once per run, not once per message.
              if (senderName != null && !isOwn && startsRun)
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
              // The quote sits above everything, the way a reply reads: context first, then the reply.
              if (quote != null) quote!,

              if (photos != null) ...[
                photos!,
                // A caption gets air above it. A photo with no caption gets none — the gap would be
                // the only thing in the bubble.
                if (body != null && body!.isNotEmpty) const SizedBox(height: 6),
              ],

              // A photo with no caption has no body line at all. An empty Text still takes a row of
              // leading and leaves a strip of dead space under the picture.
              if (photos == null || body == null || body!.isNotEmpty)
                _Body(body: body, l10n: l10n),

              // Likewise the timestamp: a run of five messages sent in the same minute does not need
              // five identical clocks down its side.
              if (endsRun) ...[
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
            ],
          ),
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
