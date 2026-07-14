// One open conversation: the feed, the decryption, and the composer.
//
// The feed is a REVERSED ListView. That is not a styling choice — it is what makes messages
// bottom-align when there are few of them, keeps the newest in view, and holds scroll position when
// an older page is prepended, all without the manual scroll-anchoring the web has to do.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../crypto/mls_errors.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../calls/call_controller.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_scaffold.dart';
import '../widgets/error_view.dart';
import 'chat_providers.dart';
import 'chat_time.dart';
import 'conversation_title.dart';
import 'group_members_sheet.dart';
import 'message_feed_controller.dart';
import 'safety_number_sheet.dart';
import 'widgets/call_event_bubble.dart';
import 'widgets/conversation_avatar.dart';
import 'widgets/message_bubble.dart';

class ConversationChatPage extends ConsumerWidget {
  const ConversationChatPage({super.key, required this.conversationId});

  final String conversationId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final conversation = ref.watch(conversationProvider(conversationId));

    return conversation.when(
      loading: () =>
          const AdaptiveScaffold(body: Center(child: AdaptiveProgress())),
      error: (e, _) => AdaptiveScaffold(
        body: ErrorView(
          message: e.toString(),
          onRetry: () => ref.invalidate(conversationProvider(conversationId)),
        ),
      ),
      data: (c) => _ChatView(conversation: c),
    );
  }
}

class _ChatView extends ConsumerStatefulWidget {
  const _ChatView({required this.conversation});

  final Conversation conversation;

  @override
  ConsumerState<_ChatView> createState() => _ChatViewState();
}

class _ChatViewState extends ConsumerState<_ChatView> {
  final _composer = TextEditingController();
  final _scroll = ScrollController();
  bool _sending = false;

  String get _conversationId => widget.conversation.id;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
  }

  @override
  void dispose() {
    _scroll.removeListener(_onScroll);
    _scroll.dispose();
    _composer.dispose();
    super.dispose();
  }

  /// The feed is reversed, so "scrolled near the end" means "scrolled back into history".
  void _onScroll() {
    if (_scroll.position.pixels > _scroll.position.maxScrollExtent - 300) {
      ref.read(messageFeedProvider(_conversationId).notifier).loadOlder();
    }
  }

  Future<void> _send() async {
    final body = _composer.text.trim();
    if (body.isEmpty || _sending) return;

    setState(() => _sending = true);
    try {
      await ref
          .read(messageFeedProvider(_conversationId).notifier)
          .send(widget.conversation, body);
      if (!mounted) return;
      _composer.clear();
    } on Object catch (e) {
      if (!mounted) return;
      final l10n = AppLocalizations.of(context);
      // "Not in the group yet" is not a send failure — it is a device still being admitted, and it
      // resolves on its own. Saying "could not send" would send the user hunting for a fault.
      final message = switch (e) {
        NotInGroupException() => l10n.t('chat.notJoined'),
        PeerKeysMissingException() => l10n.t('chat.peerNotReady'),
        _ => l10n.t('chat.sendFailed'),
      };
      notifyError(context, message, e);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _placeCall() async {
    try {
      await ref.read(callProvider.notifier).place(_conversationId);
    } on Object catch (e) {
      if (!mounted) return;
      notifyError(context, AppLocalizations.of(context).t('call.failed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final conversation = widget.conversation;
    final myUserId = ref.watch(myUserIdProvider);
    final feed = ref.watch(messageFeedProvider(_conversationId));
    final title = conversationTitle(conversation, myUserId, l10n);

    // The server answers 503 when it has no TURN, and that is not a transient failure — it is how the
    // client learns not to offer a call button at all.
    final callingAvailable = ref.watch(callingAvailableProvider).value ?? false;
    final onACall = ref.watch(callProvider) != null;

    final other = conversation.otherMember(myUserId);
    final avatarId = conversation.isGroup
        ? conversation.id
        : (other?.userId ?? conversation.id);

    return AdaptiveScaffold(
      title: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          ConversationAvatar(id: avatarId, label: title, size: 32),
          const SizedBox(width: 8),
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600),
                ),
                if (conversation.isGroup)
                  Text(
                    l10n.tp('chat.memberCount', {
                      'count': '${conversation.members.length}',
                    }),
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
              ],
            ),
          ),
        ],
      ),
      trailing: [
        // 1:1 only — the call code is a two-party exchange, and a group call would need signed
        // signalling (every member can derive every other member's key, which gives group
        // authenticity but not sender authenticity). Hidden entirely when the server has no TURN
        // configured, rather than offered as a button that cannot work.
        if (!conversation.isGroup && callingAvailable && !onACall)
          AdaptiveIconButton(
            icon: Icons.call_outlined,
            semanticLabel: l10n.t('call.start'),
            onPressed: feed.joined ? () => _placeCall() : null,
          ),
        AdaptiveIconButton(
          icon: Icons.lock_outline,
          semanticLabel: l10n.t('safety.verify'),
          onPressed: () => showSafetyNumberSheet(context, _conversationId),
        ),
        if (conversation.isGroup)
          AdaptiveIconButton(
            icon: Icons.group_outlined,
            semanticLabel: l10n.t('group.membersTitle'),
            onPressed: () => showGroupMembersSheet(context, conversation),
          ),
      ],
      body: Column(
        children: [
          Expanded(
            child: _Feed(
              feed: feed,
              conversation: conversation,
              myUserId: myUserId,
            ),
          ),
          _Composer(
            controller: _composer,
            sending: _sending,
            onSend: _send,
            notice: _notice(feed, l10n),
          ),
        ],
      ),
    );
  }

  /// The one line above the composer that explains why sending may not work yet. There are exactly
  /// two reasons, and they are different problems with different resolutions.
  String? _notice(MessageFeedState feed, AppLocalizations l10n) {
    if (feed.peerNotReady) return l10n.t('chat.peerNotReady');
    if (!feed.joined) return l10n.t('chat.joiningOnThisDevice');
    return null;
  }
}

class _Feed extends ConsumerWidget {
  const _Feed({
    required this.feed,
    required this.conversation,
    required this.myUserId,
  });

  final MessageFeedState feed;
  final Conversation conversation;
  final String myUserId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    if (feed.loading && feed.messages.isEmpty) {
      return const Center(child: AdaptiveProgress());
    }
    if (feed.messages.isEmpty) {
      return Center(
        child: Text(
          l10n.t('chat.noChatMessages'),
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      );
    }

    // Newest first, because the list is reversed.
    final items = feed.messages.reversed.toList(growable: false);

    return ListView.builder(
      reverse: true,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      itemCount: items.length + (feed.loadingOlder ? 1 : 0),
      itemBuilder: (context, i) {
        if (i >= items.length) {
          return const Padding(
            padding: EdgeInsets.all(12),
            child: Center(child: AdaptiveProgress(size: 18)),
          );
        }

        final message = items[i];
        // The list runs newest-first, so the message BELOW this one on screen is the next index.
        final older = i + 1 < items.length ? items[i + 1] : null;
        final day = messageDay(message.createdAt);
        final olderDay = older == null ? null : messageDay(older.createdAt);
        final startsDay = day != null && (olderDay == null || day != olderDay);

        return Column(
          children: [
            if (startsDay) DateSeparator(day: day),
            _Message(
              message: message,
              conversation: conversation,
              myUserId: myUserId,
              body: feed.bodies[message.id],
            ),
          ],
        );
      },
    );
  }
}

class _Message extends StatelessWidget {
  const _Message({
    required this.message,
    required this.conversation,
    required this.myUserId,
    required this.body,
  });

  final ChatMessage message;
  final Conversation conversation;
  final String myUserId;
  final String? body;

  @override
  Widget build(BuildContext context) {
    final isOwn = message.senderId == myUserId;

    if (message.contentType == ContentType.callEvent) {
      final outcome = CallOutcome.parse(body);
      if (outcome == null) return const SizedBox.shrink();
      return CallEventBubble(
        outcome: outcome,
        createdAt: message.createdAt,
        isOwn: isOwn,
      );
    }

    // In a group, other people's messages carry a name — there is more than one "them".
    String? senderName;
    if (conversation.isGroup && !isOwn) {
      final member = conversation.memberOf(message.senderId);
      senderName = member == null ? null : userLabel(member.user);
    }

    return MessageBubble(
      body: body,
      createdAt: message.createdAt,
      isOwn: isOwn,
      senderName: senderName,
    );
  }
}

class _Composer extends StatelessWidget {
  const _Composer({
    required this.controller,
    required this.sending,
    required this.onSend,
    required this.notice,
  });

  final TextEditingController controller;
  final bool sending;
  final VoidCallback onSend;
  final String? notice;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    return SafeArea(
      top: false,
      child: Container(
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(color: theme.dividerColor.withValues(alpha: 0.5)),
          ),
        ),
        padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
        child: Column(
          children: [
            if (notice != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Row(
                  children: [
                    Icon(
                      Icons.lock_outline,
                      size: 16,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        notice!,
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: TextField(
                    controller: controller,
                    minLines: 1,
                    maxLines: 6,
                    textInputAction: TextInputAction.newline,
                    keyboardType: TextInputType.multiline,
                    decoration: InputDecoration(
                      hintText: l10n.t('chat.composerPlaceholder'),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(20),
                        borderSide: BorderSide.none,
                      ),
                      filled: true,
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 14,
                        vertical: 10,
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                IconButton.filled(
                  onPressed: sending ? null : onSend,
                  icon: sending
                      ? const AdaptiveProgress(size: 16)
                      : const Icon(Icons.arrow_upward, size: 20),
                  tooltip: l10n.t('chat.send'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
