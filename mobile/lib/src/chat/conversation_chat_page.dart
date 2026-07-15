// One open conversation: the feed, the decryption, and the composer.
//
// The feed is a REVERSED ListView. That is not a styling choice — it is what makes messages
// bottom-align when there are few of them, keeps the newest in view, and holds scroll position when
// an older page is prepended, all without the manual scroll-anchoring the web has to do.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

import '../crypto/chat_content.dart';

import '../core/providers.dart';
import '../core/snackbar.dart';
import '../crypto/mls_errors.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../calls/call_controller.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_feedback.dart';
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
import 'widgets/photo_grid.dart';
import 'widgets/reply_quote.dart';

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

/// How many photos may ride on one message. Telegram's album is ten; so is this.
const _maxPhotos = 10;

class _ChatViewState extends ConsumerState<_ChatView> {
  final _composer = TextEditingController();
  final _scroll = ScrollController();
  bool _sending = false;

  /// Whether the view is pinned to the newest message. The list is reversed, so "at the bottom" is
  /// offset zero.
  bool _atBottom = true;

  /// Messages that have arrived while the user was reading back through history.
  int _unseen = 0;

  /// Photos picked but not yet sent, as decoded bytes.
  ///
  /// image_picker is asked to resize and re-encode, which is not only about size: reading the picture
  /// back out of the platform's own encoder STRIPS THE METADATA. A phone photo routinely carries the
  /// GPS coordinates of where it was taken inside its EXIF block, and shipping an end-to-end encrypted
  /// photo with the sender's home address inside it would be a fine joke at our expense.
  final _photos = <Uint8List>[];

  /// The message being replied to, if any.
  ChatMessage? _replyingTo;

  String get _conversationId => widget.conversation.id;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
    // The send button's enabled state is derived from the composer's text, so it has to rebuild when
    // that text changes. The composer is stateless and does not watch its own controller, so without
    // this the button only re-enables when the view happens to rebuild for another reason — and typing
    // a message would leave Send stubbornly greyed.
    _composer.addListener(_onComposerChanged);
  }

  void _onComposerChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _scroll.removeListener(_onScroll);
    _composer.removeListener(_onComposerChanged);
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

  /// Picks photos. Resized and re-encoded by the platform, which is what strips the EXIF.
  Future<void> _attach() async {
    try {
      final picked = await ImagePicker().pickMultiImage(
        maxWidth: 1600,
        maxHeight: 1600,
        imageQuality: 82,
        limit: _maxPhotos - _photos.length,
      );
      if (picked.isEmpty || !mounted) return;

      final bytes = <Uint8List>[];
      for (final file in picked) {
        bytes.add(await file.readAsBytes());
      }
      if (!mounted) return;

      setState(() {
        _photos.addAll(bytes);
        if (_photos.length > _maxPhotos) {
          _photos.removeRange(_maxPhotos, _photos.length);
        }
      });
    } on Object catch (e) {
      if (!mounted) return;
      notifyError(
        context,
        AppLocalizations.of(context).t('chat.photoFailed'),
        e,
      );
    }
  }

  void _startReply(ChatMessage message) {
    setState(() => _replyingTo = message);
  }

  Future<void> _send() async {
    final body = _composer.text.trim();
    // A photo with no caption is a perfectly good message.
    if ((body.isEmpty && _photos.isEmpty) || _sending) return;

    final photos = List<Uint8List>.from(_photos);
    final replyTo = _replyingTo?.id;

    setState(() => _sending = true);
    try {
      await ref
          .read(messageFeedProvider(_conversationId).notifier)
          .send(widget.conversation, body, replyTo: replyTo, photos: photos);
      if (!mounted) return;

      _composer.clear();
      setState(() {
        _photos.clear();
        _replyingTo = null;
      });
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
            // Enabled the moment we know we hold the group — which, on a chat this device has
            // opened before, is the first frame.
            onPressed: feed.joined == true ? () => _placeCall() : null,
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
        PopupMenuButton<String>(
          icon: const Icon(Icons.more_vert),
          tooltip: l10n.t('chat.conversationMenu'),
          onSelected: (value) {
            if (value == 'delete') {
              _confirmDeleteConversation(context, ref, conversation);
            } else if (value == 'leave') {
              _confirmLeaveGroup(context, ref, conversation, myUserId);
            }
          },
          itemBuilder: (context) => [
            // A direct chat: either party may delete it. A group: only an admin deletes it for
            // everyone; a plain member can leave instead.
            if (!conversation.isGroup || conversation.isAdmin(myUserId))
              PopupMenuItem<String>(
                value: 'delete',
                child: Text(
                  conversation.isGroup
                      ? l10n.t('group.deleteGroup')
                      : l10n.t('chat.deleteChat'),
                ),
              )
            else
              PopupMenuItem<String>(
                value: 'leave',
                child: Text(l10n.t('group.leave')),
              ),
          ],
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
                  onReply: _startReply,
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
            onAttach: _photos.length >= _maxPhotos ? () {} : _attach,
            photos: _photos,
            onRemovePhoto: (i) => setState(() => _photos.removeAt(i)),
            replyingTo: _replyingTo,
            replyAuthor: _replyAuthor(conversation),
            replyText: _replyText(feed, l10n),
            onCancelReply: () => setState(() => _replyingTo = null),
          ),
        ],
      ),
    );
  }

  /// Who wrote the message being replied to.
  String? _replyAuthor(Conversation conversation) {
    final replyingTo = _replyingTo;
    if (replyingTo == null) return null;
    final member = conversation.memberOf(replyingTo.senderId);
    return member == null ? null : userLabel(member.user);
  }

  /// The text of the message being replied to. Null when this device cannot read it — which is a real
  /// state, not a loading one, and ReplyQuote says so.
  String? _replyText(MessageFeedState feed, AppLocalizations l10n) {
    final replyingTo = _replyingTo;
    if (replyingTo == null) return null;

    final content = feed.contents[replyingTo.id];
    if (content == null) return null;
    if (content.body.isNotEmpty) return content.body;
    return content.hasPhotos ? l10n.t('chat.photo') : '';
  }

  /// The rule lives in message_feed_controller.dart, next to the state it reads, so this and its test
  /// cannot drift apart.
  String? _notice(MessageFeedState feed, AppLocalizations l10n) {
    final key = feedNoticeKey(feed);
    return key == null ? null : l10n.t(key);
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
    required this.onReply,
  });

  final MessageFeedState feed;
  final Conversation conversation;
  final String myUserId;
  final ScrollController scroll;
  final void Function(ChatMessage) onReply;

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
              content: feed.contents[message.id],
              feed: feed,
              onReply: onReply,
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

class _Message extends ConsumerWidget {
  const _Message({
    required this.message,
    required this.conversation,
    required this.myUserId,
    required this.content,
    required this.feed,
    required this.onReply,
    required this.startsRun,
    required this.endsRun,
  });

  final ChatMessage message;
  final Conversation conversation;
  final String myUserId;

  /// Null when this device cannot read the message at all.
  final ChatContent? content;

  /// Needed to resolve a reply's quote from a message we already hold.
  final MessageFeedState feed;

  final void Function(ChatMessage) onReply;
  final bool startsRun;
  final bool endsRun;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isOwn = message.senderId == myUserId;

    if (message.contentType == ContentType.callEvent) {
      final outcome = CallOutcome.parse(content?.body);
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

    final photos = content?.photos ?? const <ChatPhoto>[];

    return MessageBubble(
      body: content?.body,
      createdAt: message.createdAt,
      isOwn: isOwn,
      senderName: senderName,
      startsRun: startsRun,
      endsRun: endsRun,
      onLongPress: content == null
          ? null
          : () => _showActions(context, ref, content!),
      quote: _quote(context),
      photos: photos.isEmpty
          ? null
          : PhotoGrid(conversationId: conversation.id, photos: photos),
    );
  }

  /// The quoted message, rendered from what THIS DEVICE already holds — never from text copied into
  /// the reply. A sender who could supply the quote could quote you as saying anything at all.
  Widget? _quote(BuildContext context) {
    final replyTo = content?.replyTo;
    if (replyTo == null) return null;

    ChatMessage? quoted;
    for (final m in feed.messages) {
      if (m.id == replyTo) {
        quoted = m;
        break;
      }
    }

    // We do not hold it: it was sent before this device joined the group, and no amount of waiting
    // will change that. ReplyQuote says so rather than pretending to load.
    final quotedContent = quoted == null ? null : feed.contents[quoted.id];

    String? author;
    if (quoted != null) {
      final member = conversation.memberOf(quoted.senderId);
      author = member == null ? null : userLabel(member.user);
    }

    return ReplyQuote(author: author, text: _quoteText(context, quotedContent));
  }

  /// What a quote shows. A photo with no caption still has to say something.
  String? _quoteText(BuildContext context, ChatContent? quoted) {
    if (quoted == null) return null;
    if (quoted.body.isNotEmpty) return quoted.body;
    if (quoted.hasPhotos) return AppLocalizations.of(context).t('chat.photo');
    return '';
  }

  void _showActions(BuildContext context, WidgetRef ref, ChatContent content) {
    final l10n = AppLocalizations.of(context);
    HapticFeedback.mediumImpact();

    showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheet) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.reply_outlined),
              title: Text(l10n.t('chat.reply')),
              onTap: () {
                Navigator.of(sheet).pop();
                onReply(message);
              },
            ),
            if (content.body.isNotEmpty)
              ListTile(
                leading: const Icon(Icons.copy_outlined),
                title: Text(l10n.t('common.copy')),
                onTap: () async {
                  await Clipboard.setData(ClipboardData(text: content.body));
                  if (!sheet.mounted) return;
                  Navigator.of(sheet).pop();
                  notifySuccess(context, l10n.t('common.copied'));
                },
              ),
          ],
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
    required this.onAttach,
    required this.photos,
    required this.onRemovePhoto,
    required this.replyingTo,
    required this.replyAuthor,
    required this.replyText,
    required this.onCancelReply,
  });

  final TextEditingController controller;
  final bool sending;
  final VoidCallback onSend;
  final String? notice;

  final VoidCallback onAttach;

  /// Photos picked but not yet sent. Held as decoded bytes so the strip can show them without
  /// touching the disk again.
  final List<Uint8List> photos;
  final void Function(int) onRemovePhoto;

  final ChatMessage? replyingTo;
  final String? replyAuthor;
  final String? replyText;
  final VoidCallback onCancelReply;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    // Something to send: text, or a photo, or both. A photo with no caption is a perfectly good
    // message, so an empty box must not disable the button when a picture is attached.
    final canSend =
        !sending && (controller.text.trim().isNotEmpty || photos.isNotEmpty);

    return SafeArea(
      top: false,
      child: Container(
        decoration: BoxDecoration(
          border: Border(
            top: BorderSide(color: theme.dividerColor.withValues(alpha: 0.5)),
          ),
        ),
        padding: const EdgeInsets.fromLTRB(8, 8, 8, 8),
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

            // What you are replying to, above the box you are typing into — so the context is where
            // the eye already is.
            if (replyingTo != null)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Row(
                  children: [
                    Expanded(
                      child: ReplyQuote(
                        author: replyAuthor,
                        text: replyText,
                        compact: true,
                      ),
                    ),
                    IconButton(
                      onPressed: onCancelReply,
                      icon: const Icon(Icons.close, size: 18),
                      tooltip: l10n.t('common.cancel'),
                    ),
                  ],
                ),
              ),

            if (photos.isNotEmpty)
              SizedBox(
                height: 72,
                child: ListView.separated(
                  scrollDirection: Axis.horizontal,
                  padding: const EdgeInsets.only(bottom: 8),
                  itemCount: photos.length,
                  separatorBuilder: (_, _) => const SizedBox(width: 6),
                  itemBuilder: (context, i) => _PickedPhoto(
                    bytes: photos[i],
                    onRemove: () => onRemovePhoto(i),
                  ),
                ),
              ),

            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                IconButton(
                  onPressed: sending ? null : onAttach,
                  icon: const Icon(Icons.photo_outlined),
                  tooltip: l10n.t('chat.attachPhoto'),
                ),
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
                  onPressed: canSend ? onSend : null,
                  // The default filled button paints its icon in onPrimary, but on this theme the
                  // arrow came out near-black on the purple fill — unreadable. Pin it to onPrimary so
                  // both the arrow and the sending spinner stay legible against the fill.
                  style: IconButton.styleFrom(
                    foregroundColor: theme.colorScheme.onPrimary,
                    disabledForegroundColor: theme.colorScheme.onSurfaceVariant
                        .withValues(alpha: 0.5),
                  ),
                  icon: sending
                      ? SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: theme.colorScheme.onPrimary,
                          ),
                        )
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

/// A photo waiting to be sent.
class _PickedPhoto extends StatelessWidget {
  const _PickedPhoto({required this.bytes, required this.onRemove});

  final Uint8List bytes;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    return Stack(
      clipBehavior: Clip.none,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: Image.memory(bytes, width: 64, height: 64, fit: BoxFit.cover),
        ),
        Positioned(
          top: -6,
          right: -6,
          child: IconButton(
            onPressed: onRemove,
            iconSize: 16,
            visualDensity: VisualDensity.compact,
            style: IconButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.surface,
            ),
            icon: const Icon(Icons.close),
          ),
        ),
      ],
    );
  }
}

/// Confirms and deletes a conversation, then returns to the chat list.
///
/// A direct chat is deleted for both people; a group, by an admin, for everyone. The server enforces
/// who may do this — the menu only offers it where it is allowed — and the delete is irreversible, so
/// it is always behind a confirm.
Future<void> _confirmDeleteConversation(
  BuildContext context,
  WidgetRef ref,
  Conversation conversation,
) async {
  final l10n = context.l10n;
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
    // Goes through the list controller, not the repo directly: it deletes server-side AND drops the
    // conversation from the list state and the local caches, so it does not linger in the list.
    await ref.read(conversationListProvider.notifier).delete(conversation.id);
    if (!context.mounted) return;
    notifySuccess(context, l10n.t('chat.chatDeleted'));
    context.go('/');
  } on Object catch (e) {
    if (context.mounted) notifyError(context, l10n.t('chat.deleteFailed'), e);
  }
}

/// Confirms and leaves a group as a plain member: drops this device's membership and returns to the
/// chat list. Leaving is not an MLS Commit — the members who remain prune the leaf on their next
/// reconcile (see MlsService) — so all this does is the server-side membership drop.
Future<void> _confirmLeaveGroup(
  BuildContext context,
  WidgetRef ref,
  Conversation conversation,
  String myUserId,
) async {
  final l10n = context.l10n;
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
        .read(repositoryProvider)
        .removeConversationMember(conversation.id, myUserId);
    if (!context.mounted) return;
    context.go('/');
  } on Object catch (e) {
    if (context.mounted) notifyError(context, l10n.t('chat.deleteFailed'), e);
  }
}
