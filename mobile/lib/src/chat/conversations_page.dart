// The conversation list.

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_refresh.dart';
import '../widgets/adaptive/adaptive_scaffold.dart';
import '../widgets/adaptive/adaptive_search_field.dart';
import '../widgets/brand_logo.dart';
import '../widgets/scroll_hiding_header.dart';
import '../widgets/adaptive/platform.dart';
import '../widgets/error_view.dart';
import 'chat_providers.dart';
import 'chat_time.dart';
import 'conversation_title.dart';
import 'new_chat_sheet.dart';
import 'dart:async';

import 'widgets/conversation_avatar.dart';
import '../push/conversation_shortcuts.dart';

/// How many conversations get a notification shortcut. Android bounds the number of dynamic
/// shortcuts an app may publish, so this spends that budget on the most recent chats.
const _maxConversationShortcuts = 15;

class ConversationsPage extends ConsumerStatefulWidget {
  const ConversationsPage({super.key});

  @override
  ConsumerState<ConversationsPage> createState() => _ConversationsPageState();
}

class _ConversationsPageState extends ConsumerState<ConversationsPage> {
  final _search = TextEditingController();
  String _query = '';

  /// Conversations whose notification shortcut has already been published this run.
  ///
  /// Publishing is what lets Android draw a message notification as a conversation — the sender's
  /// avatar as the icon, the app badged into its corner — and it has to happen HERE rather than
  /// when the notification arrives. Most notifications are drawn by the background isolate, which
  /// cannot reach the platform channel that publishes them, so by then it is too late. Seeing a
  /// conversation in the list is the earliest reliable moment.
  ///
  /// The picture comes from the notification itself, not from the shortcut, so a name is enough
  /// here — which is just as well, since this list draws initials rather than fetching avatars.
  final _shortcutsPublished = <String>{};

  void _publishShortcuts(
    List<Conversation> conversations,
    String myUserId,
    AppLocalizations l10n,
  ) {
    for (final c in conversations.take(_maxConversationShortcuts)) {
      if (!_shortcutsPublished.add(c.id)) continue;
      unawaited(
        ConversationShortcuts.publish(
          conversationId: c.id,
          name: conversationTitle(c, myUserId, l10n),
        ),
      );
    }
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  /// Filters on the title, which is the only thing the list can search.
  ///
  /// Not the messages. The server holds nothing but ciphertext, so it cannot search them for us, and
  /// this device can only read the bodies it has decrypted itself — so a message search would silently
  /// cover a different slice of history on every device the user owns. Better to search what is
  /// honestly searchable than to offer a search that quietly lies about its scope.
  List<Conversation> _filter(
    List<Conversation> all,
    String myUserId,
    AppLocalizations l10n,
  ) {
    if (_query.isEmpty) return all;
    final needle = _query.toLowerCase();
    return all
        .where(
          (c) => conversationTitle(
            c,
            myUserId,
            l10n,
          ).toLowerCase().contains(needle),
        )
        .toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final conversations = ref.watch(conversationListProvider);
    final myUserId = ref.watch(myUserIdProvider);
    // Android caps how many dynamic shortcuts an app may hold, and a long list would spend that cap
    // on chats nobody is waiting on. The most recent ones are the ones a notification is likely to
    // be about.
    conversations.whenData(
      (list) => _publishShortcuts(list, myUserId, AppLocalizations.of(context)),
    );

    return AdaptiveScaffold(
      // A TAB, not a pushed route — see the same note on ChannelsPage.
      transitionBetweenRoutes: false,
      // The same brand mark the Channels tab shows. The two are one app and one screen with
      // different contents; titling one with a logo and the other with the word "Chats" made them
      // look like different products sharing a tab bar.
      title: const BrandLogo(size: 26),
      trailing: [
        // iOS keeps the button in the bar: a floating action button is a Material idiom and looks
        // imported on a Cupertino screen. Android gets the labelled button at the bottom instead —
        // see floatingActionButton below, and Channels, which this now matches.
        if (isCupertino(context))
          AdaptiveIconButton(
            icon: Icons.add,
            semanticLabel: l10n.t('chat.newChat'),
            onPressed: () => showNewChatSheet(context),
          ),
        // Settings used to live only on the Channels tab, so someone who only uses Chats had no
        // route to them at all — including to the notification-preview setting, which is about
        // chats. /settings is a top-level route; both tabs can reach the same screen.
        AdaptiveIconButton(
          icon: isCupertino(context)
              ? CupertinoIcons.settings
              : Icons.settings_outlined,
          semanticLabel: l10n.t('common.settings'),
          onPressed: () => context.push('/settings'),
        ),
      ],
      // A labelled button where the Channels tab has one, rather than a bare "+" in the far
      // corner. The two tabs are the same screen with different contents; putting their primary
      // action in different places, with different affordances, made them look unrelated.
      floatingActionButton: isCupertino(context)
          ? null
          : FloatingActionButton.extended(
              onPressed: () => showNewChatSheet(context),
              icon: const Icon(Icons.add),
              label: Text(l10n.t('chat.newChat')),
            ),
      body: ScrollHidingHeader(
        header:
            // Hidden when there is nothing to search — an empty account was offered a search box
            // above the words "No chats yet", which asks the user to look for something the screen
            // has just told them does not exist.
            //
            // It stays while a search is RUNNING, however empty the result. Hiding it the moment a
            // query matched nothing would take away the only means of clearing that query, and the
            // screen would be stuck showing "Nothing found" with no way back.
            ((conversations.value?.isNotEmpty ?? false) || _query.isNotEmpty)
            ? Padding(
                padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
                child: AdaptiveSearchField(
                  controller: _search,
                  placeholder: l10n.t('chat.search'),
                  onChanged: (v) => setState(() => _query = v.trim()),
                ),
              )
            : const SizedBox.shrink(),
        child: conversations.when(
          loading: () => const Center(child: AdaptiveProgress()),
          error: (e, _) => ErrorView(
            message: e.toString(),
            onRetry: () =>
                ref.read(conversationListProvider.notifier).refresh(),
          ),
          data: (all) {
            final list = _filter(all, myUserId, l10n);

            return AdaptiveRefreshableScrollView(
              onRefresh: () =>
                  ref.read(conversationListProvider.notifier).refresh(),
              slivers: [
                if (list.isEmpty)
                  SliverFillRemaining(
                    hasScrollBody: false,
                    child: _EmptyState(
                      l10n: l10n,
                      // "Nothing found" is a different thing from "no chats yet", and telling a
                      // user to start a chat when they have twenty and mistyped a name is noise.
                      searching: _query.isNotEmpty,
                    ),
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
            );
          },
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.l10n, this.searching = false});

  final AppLocalizations l10n;

  /// Whether the list is empty because a search matched nothing, rather than because there is nothing.
  final bool searching;

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
              searching ? Icons.search_off : Icons.forum_outlined,
              size: 44,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(height: 12),
            Text(
              l10n.t(searching ? 'chat.noResults' : 'chat.noChats'),
              style: theme.textTheme.bodyMedium?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            if (!searching) ...[
              const SizedBox(height: 4),
              Text(
                l10n.t('chat.pickChatHint'),
                textAlign: TextAlign.center,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
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
    // A photo with no caption. The cache writes a marker rather than an empty string, because an empty
    // row reads as a bug rather than as a picture.
    if (cached == '__photo__') return l10n.t('chat.photo');
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
            ConversationAvatar(
              id: avatarId,
              label: title,
              imageUrl: conversationAvatarUrl(
                isGroup: conversation.isGroup,
                groupAvatarId: conversation.avatarId,
                otherAvatarId: other?.user.avatarId,
                toUrl: ref.read(repositoryProvider).imageUrl,
              ),
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
