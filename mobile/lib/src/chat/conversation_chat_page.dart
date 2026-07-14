// One open conversation: the feed, the decryption, and the composer.
//
// The feed is a REVERSED ListView. That is not a styling choice — it is what makes messages
// bottom-align when there are few of them, keeps the newest in view, and holds scroll position when
// an older page is prepended, all without the manual scroll-anchoring the web has to do.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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

/// Past this far back in the history, a new message must NOT yank the view down.
///
/// Somebody reading a month of scrollback does not want to be dragged to the bottom because a message
/// arrived. Telegram's answer — and this one — is to leave them where they are and put a button on
/// screen instead, with a count of what they have not seen.
const _stickToBottom = 120.0;

class _ChatViewState extends ConsumerState<_ChatView> {
  final _composer = TextEditingController();
  final _scroll = ScrollController();
  bool _sending = false;

  /// Whether the view is pinned to the newest message. The list is reversed, so "at the bottom" is
  /// offset zero.
  bool _atBottom = true;

  /// Messages that have arrived while the user was reading back through history.
  int _unseen = 0;

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

  /// The feed is REVERSED. Two consequences, and both are easy to get backwards:
  ///   * offset 0 is the NEWEST message, not the oldest;
  ///   * scrolling toward maxScrollExtent goes BACK in time, which is where older pages load.
  void _onScroll() {
    if (!_scroll.hasClients) return;

    if (_scroll.position.pixels > _scroll.position.maxScrollExtent - 300) {
      ref.read(messageFeedProvider(_conversationId).notifier).loadOlder();
    }

    final atBottom = _scroll.position.pixels <= _stickToBottom;
    if (atBottom == _atBottom) return;

    setState(() {
      _atBottom = atBottom;
      if (atBottom) _unseen = 0; // they have caught up
    });
  }

  void _scrollToBottom() {
    if (!_scroll.hasClients) return;
    _scroll.animateTo(
      0,
      duration: const Duration(milliseconds: 240),
      curve: Curves.easeOutCubic,
    );
    setState(() => _unseen = 0);
  }

  /// A message arrived. Follow it only if the user was already at the bottom; otherwise count it.
  void _onNewMessage() {
    if (_atBottom) {
      // After the frame, so the list has actually grown.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scroll.hasClients) _scroll.jumpTo(0);
      });
      return;
    }
    setState(() => _unseen++);
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
      HapticFeedback.lightImpact();
      // Sending always follows your own message down. You wrote it; you want to see it land.
      _atBottom = true;
      _scrollToBottom();
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

    // A message arrived (or an older page landed). Only a message at the NEWEST end counts as
    // something the reader has not seen — an older page growing the list is them, scrolling.
    ref.listen(messageFeedProvider(_conversationId), (previous, next) {
      final before = previous?.messages.length ?? 0;
      final after = next.messages.length;
      if (after <= before) return;

      final newest = next.messages.isEmpty ? null : next.messages.last;
      final wasNewest = previous?.messages.isEmpty ?? true
          ? null
          : previous!.messages.last;
      if (newest == null || newest.id == wasNewest?.id) return;

      // Our own message is already handled by _send, which always follows it down.
      if (newest.senderId == myUserId) return;
      _onNewMessage();
    });

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
            child: Stack(
              children: [
                _Feed(
                  feed: feed,
                  conversation: conversation,
                  myUserId: myUserId,
                  scroll: _scroll,
                ),
                Positioned(
                  right: 12,
                  bottom: 12,
                  child: _JumpToBottom(
                    visible: !_atBottom,
                    unseen: _unseen,
                    onPressed: _scrollToBottom,
                  ),
                ),
              ],
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

/// The button that appears when the reader has scrolled back into history, carrying a count of what
/// has arrived since. Fades and lifts rather than popping, so it does not snatch at the eye.
class _JumpToBottom extends StatelessWidget {
  const _JumpToBottom({
    required this.visible,
    required this.unseen,
    required this.onPressed,
  });

  final bool visible;
  final int unseen;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);

    return IgnorePointer(
      ignoring: !visible,
      child: AnimatedSlide(
        offset: visible ? Offset.zero : const Offset(0, 0.3),
        duration: const Duration(milliseconds: 160),
        curve: Curves.easeOut,
        child: AnimatedOpacity(
          opacity: visible ? 1 : 0,
          duration: const Duration(milliseconds: 160),
          child: Stack(
            clipBehavior: Clip.none,
            children: [
              Material(
                elevation: 3,
                shape: const CircleBorder(),
                color: theme.colorScheme.surfaceContainerHigh,
                child: InkWell(
                  customBorder: const CircleBorder(),
                  onTap: onPressed,
                  child: const SizedBox(
                    width: 44,
                    height: 44,
                    child: Icon(Icons.arrow_downward, size: 20),
                  ),
                ),
              ),
              if (unseen > 0)
                Positioned(
                  top: -4,
                  right: -4,
                  child: Container(
                    padding: const EdgeInsets.symmetric(
                      horizontal: 6,
                      vertical: 2,
                    ),
                    constraints: const BoxConstraints(minWidth: 20),
                    decoration: BoxDecoration(
                      color: theme.colorScheme.primary,
                      borderRadius: BorderRadius.circular(999),
                    ),
                    child: Text(
                      unseen > 99 ? '99+' : '$unseen',
                      textAlign: TextAlign.center,
                      style: theme.textTheme.labelSmall?.copyWith(
                        color: theme.colorScheme.onPrimary,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

/// How far apart two messages from the same person can be and still read as one utterance.
///
/// Telegram's number is around five minutes, and it is about right: two lines typed in the same breath
/// are one thought, and two lines an hour apart are two — even from the same person, on the same day.
const _runGap = Duration(minutes: 5);

class _Feed extends ConsumerWidget {
  const _Feed({
    required this.feed,
    required this.conversation,
    required this.myUserId,
    required this.scroll,
  });

  final MessageFeedState feed;
  final Conversation conversation;
  final String myUserId;
  final ScrollController scroll;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    // A bubble-shaped skeleton rather than a spinner. It says "messages are coming" instead of
    // "something is happening", and the feed does not jolt when they land.
    if (feed.loading && feed.messages.isEmpty) return const _FeedSkeleton();

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
      controller: scroll,
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
        // The list runs newest-first, so index+1 is the message ABOVE this one on screen (older) and
        // index-1 is the one below (newer). Getting this backwards is the classic reversed-list bug.
        final older = i + 1 < items.length ? items[i + 1] : null;
        final newer = i > 0 ? items[i - 1] : null;

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
              // A new day always begins a new run, whatever the clock says.
              startsRun: startsDay || !_sameRun(older, message),
              endsRun: !_sameRun(message, newer),
            ),
          ],
        );
      },
    );
  }

  /// Whether [next] continues [previous]'s run: same sender, both ordinary messages, close in time.
  ///
  /// A call event is never part of a run — it is a system aside, not something anybody said.
  static bool _sameRun(ChatMessage? previous, ChatMessage? next) {
    if (previous == null || next == null) return false;
    if (previous.senderId != next.senderId) return false;
    if (previous.contentType == ContentType.callEvent) return false;
    if (next.contentType == ContentType.callEvent) return false;

    final a = DateTime.tryParse(previous.createdAt);
    final b = DateTime.tryParse(next.createdAt);
    if (a == null || b == null) return false;

    return b.difference(a).abs() <= _runGap;
  }
}

/// Bubble-shaped placeholders, alternating sides and widths so the feed looks like a conversation
/// before it is one.
class _FeedSkeleton extends StatelessWidget {
  const _FeedSkeleton();

  static const _widths = [0.55, 0.4, 0.68, 0.35, 0.5, 0.6];

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final width = MediaQuery.sizeOf(context).width;

    return ListView.builder(
      reverse: true,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      itemCount: _widths.length,
      itemBuilder: (context, i) {
        final own = i.isEven;
        return Align(
          alignment: own ? Alignment.centerRight : Alignment.centerLeft,
          child: Container(
            width: width * _widths[i],
            height: 40,
            margin: const EdgeInsets.symmetric(vertical: 4),
            decoration: BoxDecoration(
              color: theme.colorScheme.surfaceContainerHighest,
              borderRadius: BorderRadius.only(
                topLeft: const Radius.circular(16),
                topRight: const Radius.circular(16),
                bottomLeft: Radius.circular(own ? 16 : 2),
                bottomRight: Radius.circular(own ? 2 : 16),
              ),
            ),
          ),
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
    required this.startsRun,
    required this.endsRun,
  });

  final ChatMessage message;
  final Conversation conversation;
  final String myUserId;
  final String? body;
  final bool startsRun;
  final bool endsRun;

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
      startsRun: startsRun,
      endsRun: endsRun,
      onLongPress: body == null ? null : () => _showActions(context, body!),
    );
  }

  /// Long-press a message to copy it. The one action there is, because it is the only one the server
  /// can support: there is no reply field, no reactions, and a message cannot be edited or deleted
  /// once it is sealed.
  void _showActions(BuildContext context, String text) {
    final l10n = AppLocalizations.of(context);
    HapticFeedback.mediumImpact();

    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheet) => SafeArea(
        child: ListTile(
          leading: const Icon(Icons.copy_outlined),
          title: Text(l10n.t('common.copy')),
          onTap: () async {
            await Clipboard.setData(ClipboardData(text: text));
            if (!sheet.mounted) return;
            Navigator.of(sheet).pop();
            notifySuccess(context, l10n.t('common.copied'));
          },
        ),
      ),
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
