import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../chat/chat_time.dart';
import '../../chat/widgets/conversation_avatar.dart';
import '../../core/providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';

/// A channel in the list, drawn the way a conversation is drawn.
///
/// The two used to look nothing alike. A chat row had an avatar, what was last said, when it was
/// said and an unread dot; a channel row had a name and its raw public id — an identifier the
/// reader did not choose and cannot act on, in the space where every other row in the app puts the
/// thing that just happened. Sorting was by neither: owned channels first, then joined, each in
/// whatever order the server returned.
///
/// The one deliberate difference is the badge. A channel is a broadcast, not a conversation, and
/// the row should say so at a glance rather than by the reader noticing that nobody else ever
/// speaks in it.
class ChannelRow extends ConsumerWidget {
  const ChannelRow({super.key, required this.channel, this.role});

  final Channel channel;

  /// The reader's role, when they are a member rather than the owner. Shown as a quiet suffix
  /// rather than the loud pill the old rows carried — being an admin somewhere is worth knowing
  /// and is not what the row is about.
  final String? role;

  /// What the row says happened last.
  ///
  /// The order follows the same preference the web uses: the post's title, then its body, then a
  /// count of its photographs, and only then — for a channel nobody has posted to yet — the
  /// reference somebody would use to join it, which is the one moment that identifier is useful.
  String _preview(AppLocalizations l10n) {
    final last = channel.lastMessage;
    if (last == null) return '@${channel.joinRef}';
    if (last.title.isNotEmpty) return last.title;
    if (last.body.isNotEmpty) return last.body;
    // "Photo", whatever the count — the same word the chat list uses, and a row is not the place
    // to itemise an album.
    if (last.imageCount > 0) return l10n.t('chat.photo');
    return '@${channel.joinRef}';
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l10n = context.l10n;
    final last = channel.lastMessage;

    return InkWell(
      onTap: () => context.push('/channels/${channel.id}'),
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            // Hashed from the channel id, so a channel keeps its colour everywhere it appears —
            // the same rule the conversation avatar follows.
            Stack(
              clipBehavior: Clip.none,
              children: [
                ConversationAvatar(
                  id: channel.id,
                  label: channel.name,
                  imageUrl: channel.avatarId == null
                      ? null
                      : ref
                            .read(repositoryProvider)
                            .imageUrl(channel.avatarId!),
                ),
                Positioned(
                  right: -2,
                  bottom: -2,
                  child: Container(
                    padding: const EdgeInsets.all(2),
                    decoration: BoxDecoration(
                      color: theme.colorScheme.surface,
                      shape: BoxShape.circle,
                    ),
                    child: Icon(
                      Icons.campaign,
                      size: 13,
                      color: theme.colorScheme.primary,
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          channel.name,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodyLarge?.copyWith(
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        last == null ? '' : chatListTime(l10n, last.createdAt),
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          _preview(l10n),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ),
                      if (role != null) ...[
                        const SizedBox(width: 8),
                        Text(
                          role!,
                          style: theme.textTheme.labelSmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
