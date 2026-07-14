// The conversation list.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_refresh.dart';
import '../widgets/adaptive/adaptive_scaffold.dart';
import '../widgets/adaptive/platform.dart';
import '../widgets/error_view.dart';
import 'chat_providers.dart';
import 'chat_time.dart';
import 'conversation_title.dart';
import 'new_chat_sheet.dart';
import 'widgets/conversation_avatar.dart';

class ConversationsPage extends ConsumerWidget {
  const ConversationsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final conversations = ref.watch(conversationListProvider);

    return AdaptiveScaffold(
      title: Text(l10n.t('chat.title')),
      trailing: [
        AdaptiveIconButton(
          icon: Icons.add,
          semanticLabel: l10n.t('chat.newChat'),
          onPressed: () => showNewChatSheet(context),
        ),
      ],
      body: conversations.when(
        loading: () => const Center(child: AdaptiveProgress()),
        error: (e, _) => ErrorView(
          message: e.toString(),
          onRetry: () => ref.read(conversationListProvider.notifier).refresh(),
        ),
        data: (list) => AdaptiveRefreshableScrollView(
          onRefresh: () =>
              ref.read(conversationListProvider.notifier).refresh(),
          slivers: [
            if (list.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: _EmptyState(l10n: l10n),
              )
            else
              SliverPadding(
                padding: const EdgeInsets.symmetric(vertical: 6),
                sliver: SliverList.builder(
                  itemCount: list.length,
                  itemBuilder: (context, i) =>
                      _ConversationRow(conversation: list[i]),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.l10n});

  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    // It sits inside the refreshable scroll view rather than replacing it, so the pull-to-refresh
    // gesture still works when there is nothing to show — which is exactly when a user is most
    // likely to pull.
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.forum_outlined,
              size: 44,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(height: 12),
            Text(
              l10n.t('chat.noChats'),
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              l10n.t('chat.pickChatHint'),
              textAlign: TextAlign.center,
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

class _ConversationRow extends ConsumerWidget {
  const _ConversationRow({required this.conversation});

  final Conversation conversation;

  /// The list holds only ciphertext and cannot decrypt a thing, so a preview can only come from the
  /// local plaintext store. When it is not there — a message that arrived while the app was closed,
  /// or one from before this device joined — we say that rather than show nothing.
  String _preview(
    String? cached,
    LastChatMessage? last,
    AppLocalizations l10n,
  ) {
    if (last == null) return '';
    // Control traffic is not something a person said. It has no preview.
    if (ContentType.control.contains(last.contentType)) return '';
    if (cached != null && cached.isNotEmpty) return cached;
    return l10n.t('chat.encryptedPreview');
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final myUserId = ref.watch(myUserIdProvider);

    final title = conversationTitle(conversation, myUserId, l10n);
    final last = conversation.lastMessage;
    final preview = _preview(
      ref.watch(chatCacheProvider).preview(conversation.id),
      last,
      l10n,
    );
    final unread = ref.watch(unreadProvider(conversation));

    final other = conversation.otherMember(myUserId);
    // A direct chat takes the other person's colour, so a chat and the person in it look the same.
    final avatarId = conversation.isGroup
        ? conversation.id
        : (other?.userId ?? conversation.id);

    return InkWell(
      onTap: () => context.push('/chats/${conversation.id}'),
      borderRadius: BorderRadius.circular(8),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Row(
          children: [
            ConversationAvatar(id: avatarId, label: title),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          title,
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
                          preview,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ),
                      if (unread) ...[
                        const SizedBox(width: 8),
                        Container(
                          width: 8,
                          height: 8,
                          decoration: BoxDecoration(
                            color: theme.colorScheme.primary,
                            shape: BoxShape.circle,
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
            if (isCupertino(context))
              Icon(
                Icons.chevron_right,
                size: 18,
                color: theme.colorScheme.onSurfaceVariant,
              ),
          ],
        ),
      ),
    );
  }
}
