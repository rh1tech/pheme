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
import '../widgets/glass/glass.dart';
import '../widgets/swipe_actions.dart';
import '../core/snackbar.dart';
import '../widgets/adaptive/adaptive_feedback.dart';
import '../widgets/adaptive/platform.dart';
import '../widgets/error_view.dart';
import 'chat_providers.dart';
import 'chat_time.dart';
import 'conversation_title.dart';
import 'new_chat_sheet.dart';
import 'dart:async';

import 'widgets/call_event_bubble.dart';
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

  /// Shared by every row, so opening one row's actions closes any other. Without it a list can end
  /// up showing two delete buttons at once, and it stops being obvious which row a tap belongs to.
  final _swipe = SwipeActionsController();

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
    _swipe.dispose();
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

    // Hidden when there is nothing to search — an empty account was offered a search box above the
    // words "No chats yet", which asks the user to look for something the screen has just told them
    // does not exist.
    //
    // It stays while a search is RUNNING, however empty the result. Hiding it the moment a query
    // matched nothing would take away the only means of clearing that query, and the screen would
    // be stuck showing "Nothing found" with no way back.
    final searchable =
        (conversations.value?.isNotEmpty ?? false) || _query.isNotEmpty;

    final ios = isCupertino(context);

    return AdaptiveScaffold(
      // The list runs under the glass bar and under the floating tab bar; the scroll view spends
      // the padding the scaffold hands it, so nothing is hidden and nothing is doubly inset.
      behindChrome: true,
      // The same brand mark the Channels tab shows. The two are one app and one screen with
      // different contents; titling one with a logo and the other with the word "Chats" made them
      // look like different products sharing a tab bar.
      title: const BrandLogo(size: 26),
      // Leading, not centred, even on iOS. A centred title is right for a word — it is the name of
      // the screen you pushed into. This is a brand mark on the app's home screen, and a logo
      // floating in the middle of the bar with the controls crowded to one side reads as a mistake
      // rather than as a convention.
      centerTitle: false,
      trailing: [
        // Settings used to live only on the Channels tab, so someone who only uses Chats had no
        // route to them at all — including to the notification-preview setting, which is about
        // chats. /settings is a top-level route; both tabs can reach the same screen.
        GlassIconButton(
          icon: ios ? CupertinoIcons.settings : Icons.settings_outlined,
          semanticLabel: l10n.t('common.settings'),
          onPressed: () => context.push('/settings'),
        ),
        // The primary action lives where each platform puts it: last on the bar on iOS, and a
        // floating button on Android. This is a NAVIGATION convention rather than decoration — iOS
        // has no floating action button and one imported from Material looks it — so it is one of
        // the few places the two builds are deliberately not identical.
        if (ios)
          GlassIconButton(
            icon: CupertinoIcons.square_pencil,
            semanticLabel: l10n.t('chat.newChat'),
            onPressed: () => showNewChatSheet(context),
          ),
      ],
      floatingActionButton: ios
          ? null
          : GlassActionButton(
              icon: Icons.edit_outlined,
              semanticLabel: l10n.t('chat.newChat'),
              onPressed: () => showNewChatSheet(context),
            ),
      body: conversations.when(
        loading: () => const Center(child: AdaptiveProgress()),
        error: (e, _) => ErrorView(
          message: e.toString(),
          onRetry: () => ref.read(conversationListProvider.notifier).refresh(),
        ),
        data: (all) {
          final list = _filter(all, myUserId, l10n);

          return AdaptiveRefreshableScrollView(
            onRefresh: () =>
                ref.read(conversationListProvider.notifier).refresh(),
            slivers: [
              // In the list rather than pinned above it. A search field that hides on a scroll
              // gesture and comes back on another was one more thing moving on screen; carried by
              // the list itself it goes away when you scroll past it, comes back when you scroll
              // to the top, and never argues with the bar it sits under.
              if (searchable)
                SliverToBoxAdapter(
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(
                      GlassMetrics.gutter,
                      GlassMetrics.gap,
                      GlassMetrics.gutter,
                      4,
                    ),
                    child: AdaptiveSearchField(
                      controller: _search,
                      placeholder: l10n.t('chat.search'),
                      onChanged: (v) => setState(() => _query = v.trim()),
                    ),
                  ),
                ),
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
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  sliver: SliverList.builder(
                    itemCount: list.length,
                    itemBuilder: (context, i) =>
                        _ConversationRow(conversation: list[i], swipe: _swipe),
                  ),
                ),
            ],
          );
        },
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
  const _ConversationRow({required this.conversation, required this.swipe});

  final Conversation conversation;
  final SwipeActionsController swipe;

  /// The list holds only ciphertext and cannot decrypt a thing, so a preview can only come from the
  /// local plaintext store. When it is not there — a message that arrived while the app was closed,
  /// or one from before this device joined — we say that rather than show nothing.
  String _preview(
    String? cached,
    LastChatMessage? last,
    AppLocalizations l10n,
    String myUserId,
  ) {
    if (last == null) return '';
    // Control traffic is not something a person said. It has no preview.
    if (ContentType.control.contains(last.contentType)) return '';

    // A call event's body is JSON — `{"outcome":"failed"}` — because it has to mean something to a
    // client that has never heard of call events. It is not a sentence, and showing it raw is how a
    // failed call ended up in the chat list as a fragment of wire format.
    if (last.contentType == ContentType.callEvent) {
      final outcome = CallOutcome.parse(cached);
      if (outcome == null) return l10n.t('call.statusEnded');
      return callEventLabel(
        outcome,
        isOwn: last.senderId == myUserId,
        l10n: l10n,
      );
    }
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
      myUserId,
    );
    final unread = ref.watch(unreadProvider(conversation));

    final other = conversation.otherMember(myUserId);
    // A direct chat takes the other person's colour, so a chat and the person in it look the same.
    final avatarId = conversation.isGroup
        ? conversation.id
        : (other?.userId ?? conversation.id);

    // Whether this reader may end the conversation for everyone, or only walk away from it. Same
    // rule the chat's own menu applies — a plain member of a group cannot delete it.
    final canDelete = !conversation.isGroup || conversation.isAdmin(myUserId);

    return SwipeActions(
      controller: swipe,
      actions: [
        SwipeAction(
          label: canDelete
              ? l10n.t('common.delete')
              : l10n.t('group.leaveShort'),
          icon: canDelete ? Icons.delete_outline : Icons.logout,
          color: theme.colorScheme.error,
          onPressed: () => canDelete
              ? _deleteFromList(context, ref, conversation)
              : _leaveFromList(context, ref, conversation, myUserId),
        ),
      ],
      child: Material(
        color: theme.colorScheme.surface,
        child: InkWell(
          onTap: () => context.push('/chats/${conversation.id}'),
          child: Padding(
            // More room on the trailing edge than the leading one: the avatar is a solid shape that
            // holds its own against the screen edge, and the time is four small characters that were
            // being crowded into the corner by it.
            padding: const EdgeInsets.fromLTRB(GlassMetrics.gutter, 9, 16, 9),
            child: Row(
              // Centred, so the trailing block below sits against the middle of the row rather than
              // against the top of the title.
              crossAxisAlignment: CrossAxisAlignment.center,
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
                const SizedBox(width: GlassMetrics.gutter),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: theme.textTheme.bodyLarge?.copyWith(
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      if (preview.isNotEmpty) ...[
                        const SizedBox(height: 2),
                        Text(
                          preview,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
                // The time and the unread dot, as one block, vertically centred.
                //
                // The time used to ride on the title's own line, so it sat against the top of a
                // two-line row and hard against the chevron — and on a row with no preview it was the
                // only thing up there, floating. Centred and given room, it reads as a property of the
                // row rather than as a word appended to the name.
                if (last != null || unread) ...[
                  const SizedBox(width: 10),
                  Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.end,
                    children: [
                      if (last != null)
                        Text(
                          chatListTime(l10n, last.createdAt),
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      if (unread) ...[
                        if (last != null) const SizedBox(height: 5),
                        Container(
                          width: 9,
                          height: 9,
                          decoration: BoxDecoration(
                            color: theme.colorScheme.primary,
                            shape: BoxShape.circle,
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
                if (isCupertino(context)) ...[
                  const SizedBox(width: 8),
                  Icon(
                    Icons.chevron_right,
                    size: 18,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// Deletes a conversation from the LIST.
///
/// Deliberately not the chat page's version of this: that one navigates home afterwards, which is
/// right when you are standing inside the conversation you just deleted and wrong when you are
/// already looking at the list — it would throw away the reader's place for no reason.
Future<void> _deleteFromList(
  BuildContext context,
  WidgetRef ref,
  Conversation conversation,
) async {
  final l10n = AppLocalizations.of(context);
  final isGroup = conversation.isGroup;
  final confirmed = await showAdaptiveConfirm(
    context,
    title: l10n.t(isGroup ? 'group.deleteGroup' : 'chat.deleteChat'),
    message: l10n.t(
      isGroup ? 'group.deleteGroupConfirm' : 'chat.deleteChatConfirm',
    ),
    confirmLabel: l10n.t('common.delete'),
    cancelLabel: l10n.t('common.cancel'),
    isDestructive: true,
  );
  if (!confirmed || !context.mounted) return;

  try {
    await ref.read(conversationListProvider.notifier).delete(conversation.id);
    if (context.mounted) notifySuccess(context, l10n.t('chat.chatDeleted'));
  } on Object catch (e) {
    if (context.mounted) notifyError(context, l10n.t('chat.deleteFailed'), e);
  }
}

/// Leaves a group from the list, for a member who may not delete it for everyone.
Future<void> _leaveFromList(
  BuildContext context,
  WidgetRef ref,
  Conversation conversation,
  String myUserId,
) async {
  final l10n = AppLocalizations.of(context);
  final confirmed = await showAdaptiveConfirm(
    context,
    title: l10n.t('group.leave'),
    message: l10n.t('group.leaveConfirm'),
    confirmLabel: l10n.t('group.leave'),
    cancelLabel: l10n.t('common.cancel'),
    isDestructive: true,
  );
  if (!confirmed || !context.mounted) return;

  try {
    await ref
        .read(conversationListProvider.notifier)
        .leave(conversation.id, myUserId);
  } on Object catch (e) {
    if (context.mounted) notifyError(context, l10n.t('chat.deleteFailed'), e);
  }
}
