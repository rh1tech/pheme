// One open conversation: the feed, the decryption, and the composer.
//
// The feed is a REVERSED ListView. That is not a styling choice — it is what makes messages
// bottom-align when there are few of them, keeps the newest in view, and holds scroll position when
// an older page is prepended, all without the manual scroll-anchoring the web has to do.

import 'dart:async';

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';

import '../crypto/attribution.dart';
import '../crypto/chat_content.dart';

import '../core/api_exception.dart';
import '../core/snackbar.dart';
import '../crypto/mls_errors.dart';
import '../core/providers.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import '../calls/call_controller.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../widgets/adaptive/adaptive_feedback.dart';
import '../widgets/adaptive/adaptive_scaffold.dart';
import '../widgets/adaptive/platform.dart';
import '../widgets/error_view.dart';
import '../widgets/glass/glass.dart';
import '../widgets/measured_height.dart';
import '../widgets/photo_source_sheet.dart';
import 'chat_providers.dart';
import 'chat_time.dart';
import 'conversation_title.dart';
import 'group_members_sheet.dart';
import 'message_feed_controller.dart';
import 'receipts.dart';
import 'chat_shield_status.dart';
import 'safety_number_sheet.dart';
import 'widgets/call_event_bubble.dart';
import 'widgets/conversation_avatar.dart';
import 'widgets/chat_wallpaper.dart';
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
      // A 404 is not an error to retry: the conversation was deleted, or we were removed, while we
      // were away. Clean up and return to the list instead of offering a "try again" that never will.
      error: (e, _) => e is ApiException && e.statusCode == 404
          ? _DeletedConversationRedirect(conversationId: conversationId)
          : AdaptiveScaffold(
              body: ErrorView(
                message: e.toString(),
                onRetry: () =>
                    ref.invalidate(conversationProvider(conversationId)),
              ),
            ),
      data: (c) => _ChatView(conversation: c),
    );
  }
}

/// Handles the case where a conversation is gone on open (404). It forgets the local caches, drops
/// the row from the list, and returns to the list — the same cleanup the live `conversationDeleted`
/// event does, for when we were offline and missed it. Shows a spinner for the frame it takes.
class _DeletedConversationRedirect extends ConsumerStatefulWidget {
  const _DeletedConversationRedirect({required this.conversationId});

  final String conversationId;

  @override
  ConsumerState<_DeletedConversationRedirect> createState() =>
      _DeletedConversationRedirectState();
}

class _DeletedConversationRedirectState
    extends ConsumerState<_DeletedConversationRedirect> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      final id = widget.conversationId;
      await ref.read(chatCacheProvider).forget(id);
      await ref.read(chatEnvelopeCacheProvider).forget(id);
      await ref.read(lastSeenStoreProvider).forget(id);
      // Reconcile the list with the server, which no longer has it, rather than a targeted delete
      // that would 404 again.
      unawaited(ref.read(conversationListProvider.notifier).refresh());
      if (mounted) context.go('/');
    });
  }

  @override
  Widget build(BuildContext context) =>
      const AdaptiveScaffold(body: Center(child: AdaptiveProgress()));
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

  /// How much room the floating composer is taking, measured rather than assumed.
  ///
  /// The composer overlaps the feed now instead of sitting in a column beside it — that is what lets
  /// messages slide under it and under the bar — so the feed has to reserve exactly its height as
  /// bottom padding. It grows with a reply quote, a strip of attached photos and up to six lines of
  /// text, so there is no constant to reserve instead.
  double _composerHeight = 0;

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
    // Mark this chat as the one on screen, so the push service suppresses notifications for it —
    // a message that is already in the open feed does not also need to buzz the lock screen.
    ref.read(activeConversationIdProvider.notifier).set(_conversationId);
  }

  void _onComposerChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _scroll.removeListener(_onScroll);
    _composer.removeListener(_onComposerChanged);
    // Stop suppressing notifications for this chat — but only if it is still the one marked open. A
    // push that navigated to another chat has already claimed the slot, and clearing it here would
    // wrongly re-enable notifications for the chat now on screen.
    if (ref.read(activeConversationIdProvider) == _conversationId) {
      ref.read(activeConversationIdProvider.notifier).set(null);
    }
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

  /// Picks photos, from the camera or the library. Resized and re-encoded by the platform, which is
  /// what strips the EXIF.
  Future<void> _attach() async {
    final source = await askPhotoSource(context);
    if (source == null || !mounted) return;

    try {
      // The camera returns one shot at a time; only the library can hand over a selection.
      final picked = source == ImageSource.camera
          ? [
              ?await ImagePicker().pickImage(
                source: ImageSource.camera,
                maxWidth: 1600,
                maxHeight: 1600,
                imageQuality: 82,
              ),
            ]
          : await ImagePicker().pickMultiImage(
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

      // Our own message is already handled by _send, which always follows it down. From the
      // AUTHENTICATED sender where the feed has one: otherwise the server could suppress the
      // "new message" nudge for anything it liked simply by stamping our id on it.
      if (isOwnMessage(
        next.contents[newest.id]?.attribution,
        newest.senderId,
        myUserId,
      )) {
        return;
      }
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
      // The feed runs the full height of the screen, under the bar at the top and under the composer
      // at the bottom, which is the whole point of making both of them glass.
      behindChrome: true,
      // Leading-aligned on both platforms, against the iOS convention this bar otherwise follows: the
      // title here is a block — avatar, name, member count — and iOS centres a title by measuring the
      // space left over by the controls on either side, which for a block this wide leaves it
      // squeezed into a third of the bar.
      centerTitle: false,
      title: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          // The avatar opens the person behind it — their bio, their links, their @name.
          //
          // A direct chat only. A group's picture stands for the group, and there is no single
          // person to open; its roster is already one tap away in the menu.
          _AvatarButton(
            onPressed: other == null || conversation.isGroup
                ? null
                : () => context.push(
                    '/users/${other.userId}',
                    extra: PublicProfile.fromPublicUser(other.user),
                  ),
            child: ConversationAvatar(
              id: avatarId,
              label: title,
              size: 32,
              imageUrl: conversationAvatarUrl(
                isGroup: conversation.isGroup,
                groupAvatarId: conversation.avatarId,
                otherAvatarId: other?.user.avatarId,
                toUrl: ref.read(repositoryProvider).imageUrl,
              ),
            ),
          ),
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
      // Two controls and a menu, where there were four controls and a menu. A group chat put call,
      // lock, members and overflow in a row that ran into the title; "members" is a destination
      // rather than an action, and it reads better as the first item of the menu than as a fourth
      // circle nobody could name on sight.
      trailing: [
        // 1:1 only — the call code is a two-party exchange, and a group call would need signed
        // signalling (every member can derive every other member's key, which gives group
        // authenticity but not sender authenticity). Hidden entirely when the server has no TURN
        // configured, rather than offered as a button that cannot work.
        if (!conversation.isGroup && callingAvailable && !onACall)
          GlassIconButton(
            icon: isCupertino(context) ? CupertinoIcons.phone : Icons.call,
            semanticLabel: l10n.t('call.start'),
            // Enabled the moment we know we hold the group — which, on a chat this device has
            // opened before, is the first frame.
            onPressed: feed.joined == true ? () => _placeCall() : null,
          ),
        // The shield, tinted by what it actually knows. A lock that says "encrypted" on every chat
        // says nothing; what a person needs to know is whether the history is recoverable and
        // whether the other end is verified, and both have real answers.
        Builder(
          builder: (context) {
            final shield = ref.watch(shieldStatusProvider(_conversationId));
            return GlassIconButton(
              icon: switch (shield.level) {
                ShieldLevel.atRisk =>
                  isCupertino(context)
                      ? CupertinoIcons.exclamationmark_shield
                      : Icons.gpp_maybe_outlined,
                ShieldLevel.secure =>
                  isCupertino(context)
                      ? CupertinoIcons.lock_shield_fill
                      : Icons.verified_user_outlined,
                ShieldLevel.attention =>
                  isCupertino(context)
                      ? CupertinoIcons.lock_shield
                      : Icons.shield_outlined,
              },
              statusTint: shield.tint(Theme.of(context).colorScheme),
              semanticLabel: l10n.t('safety.verify'),
              onPressed: () => showSafetyNumberSheet(context, _conversationId),
            );
          },
        ),
        _ConversationMenu(
          conversation: conversation,
          myUserId: myUserId,
          onMembers: () => showGroupMembersSheet(context, conversation),
          onClear: () => _confirmClearHistory(context, ref, conversation),
          onDelete: () =>
              _confirmDeleteConversation(context, ref, conversation),
          onLeave: () =>
              _confirmLeaveGroup(context, ref, conversation, myUserId),
        ),
      ],
      // The wallpaper now runs edge to edge — behind the bar and behind the composer — rather than
      // stopping at the feed. It is what the glass has to blur; a bar with nothing but a flat colour
      // behind it is just a translucent rectangle.
      body: ChatWallpaper(
        child: Stack(
          children: [
            Positioned.fill(
              child: _Feed(
                feed: feed,
                conversation: conversation,
                myUserId: myUserId,
                scroll: _scroll,
                onReply: _startReply,
                // The bar at the top and the composer at the bottom, spent as content padding so
                // the first and last message clear both without the feed losing the space.
                padding: EdgeInsets.fromLTRB(
                  GlassMetrics.gutter,
                  MediaQuery.paddingOf(context).top + GlassMetrics.gap,
                  GlassMetrics.gutter,
                  _composerHeight + GlassMetrics.gap,
                ),
              ),
            ),
            Positioned(
              right: GlassMetrics.gutter,
              bottom: _composerHeight + GlassMetrics.gap,
              child: _JumpToBottom(
                visible: !_atBottom,
                unseen: _unseen,
                onPressed: _scrollToBottom,
              ),
            ),
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              child: MeasuredHeight(
                onChange: (h) {
                  if (mounted) setState(() => _composerHeight = h);
                },
                child: _Composer(
                  controller: _composer,
                  sending: _sending,
                  onSend: _send,
                  notice: _notice(feed, l10n),
                  onAttach: _photos.length >= _maxPhotos ? () {} : _attach,
                  photos: _photos,
                  onRemovePhoto: (i) => setState(() => _photos.removeAt(i)),
                  replyingTo: _replyingTo,
                  replyAuthor: _replyAuthor(conversation, feed),
                  replyText: _replyText(feed, l10n),
                  onCancelReply: () => setState(() => _replyingTo = null),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// Who wrote the message being replied to.
  ///
  /// From the AUTHENTICATED sender where this device has read the message. Naming a reply's target
  /// from the envelope would let the server decide whose words you appear to be answering — and the
  /// quote that goes out carries only the message id, so the recipient renders it from their own
  /// copy and would see a different name than the one shown here.
  String? _replyAuthor(Conversation conversation, MessageFeedState feed) {
    final replyingTo = _replyingTo;
    if (replyingTo == null) return null;
    final view = resolveAuthor(
      feed.contents[replyingTo.id]?.attribution,
      replyingTo.senderId,
    );
    if (view.tampered) return null;
    final member = conversation.memberOf(view.userId);
    return member == null ? null : userLabel(member.user);
  }

  /// The text of the message being replied to. Null when this device cannot read it — which is a real
  /// state, not a loading one, and ReplyQuote says so.
  String? _replyText(MessageFeedState feed, AppLocalizations l10n) {
    final replyingTo = _replyingTo;
    if (replyingTo == null) return null;

    final content = feed.contents[replyingTo.id]?.content;
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

/// The chat header's avatar, when it is a way in to somebody's profile.
///
/// A plain GestureDetector with a press state rather than an InkWell: a splash spreading out of a
/// 32pt circle at the top of the bar draws more attention than the tap deserves, and the rest of the
/// bar's controls already sink rather than splash.
class _AvatarButton extends StatefulWidget {
  const _AvatarButton({required this.child, required this.onPressed});

  final Widget child;
  final VoidCallback? onPressed;

  @override
  State<_AvatarButton> createState() => _AvatarButtonState();
}

class _AvatarButtonState extends State<_AvatarButton> {
  bool _down = false;

  void _set(bool down) {
    if (_down != down) setState(() => _down = down);
  }

  @override
  Widget build(BuildContext context) {
    if (widget.onPressed == null) return widget.child;

    return Semantics(
      button: true,
      label: AppLocalizations.of(context).t('profile.userTitle'),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTapDown: (_) => _set(true),
        onTapUp: (_) => _set(false),
        onTapCancel: () => _set(false),
        onTap: widget.onPressed,
        child: AnimatedScale(
          scale: _down ? 0.92 : 1,
          duration: GlassMetrics.pressDuration,
          curve: Curves.easeOut,
          child: widget.child,
        ),
      ),
    );
  }
}

/// The conversation's overflow menu: everything you do TO a chat rather than in it.
///
/// Which entries exist depends on what this reader may actually do — see [GlassMenuButton] for the
/// menu itself.
class _ConversationMenu extends StatelessWidget {
  const _ConversationMenu({
    required this.conversation,
    required this.myUserId,
    required this.onMembers,
    required this.onClear,
    required this.onDelete,
    required this.onLeave,
  });

  final Conversation conversation;
  final String myUserId;
  final VoidCallback onMembers;
  final VoidCallback onClear;
  final VoidCallback onDelete;
  final VoidCallback onLeave;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    // A direct chat: either party may delete it. A group: only an admin deletes it for everyone; a
    // plain member can leave instead.
    final canDelete = !conversation.isGroup || conversation.isAdmin(myUserId);

    return GlassMenuButton(
      semanticLabel: l10n.t('chat.conversationMenu'),
      actions: [
        if (conversation.isGroup)
          GlassMenuAction(
            label: l10n.t('group.membersTitle'),
            icon: Icons.group_outlined,
            onSelected: onMembers,
          ),
        // Anyone may clear their OWN history — it is per-member and touches no one else.
        GlassMenuAction(
          label: l10n.t('chat.clearHistory'),
          icon: Icons.cleaning_services_outlined,
          onSelected: onClear,
        ),
        if (canDelete)
          GlassMenuAction(
            label: conversation.isGroup
                ? l10n.t('group.deleteGroup')
                : l10n.t('chat.deleteChat'),
            icon: Icons.delete_outline,
            destructive: true,
            onSelected: onDelete,
          )
        else
          GlassMenuAction(
            label: l10n.t('group.leave'),
            icon: Icons.logout,
            destructive: true,
            onSelected: onLeave,
          ),
      ],
    );
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
              GlassIconButton(
                icon: Icons.arrow_downward,
                semanticLabel: AppLocalizations.of(
                  context,
                ).t('chat.jumpToLatest'),
                onPressed: onPressed,
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
    required this.padding,
  });

  final MessageFeedState feed;
  final Conversation conversation;
  final String myUserId;
  final ScrollController scroll;
  final void Function(ChatMessage) onReply;

  /// Room for the chrome the feed runs underneath: the glass bar above, the composer below.
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);

    // A bubble-shaped skeleton rather than a spinner. It says "messages are coming" instead of
    // "something is happening", and the feed does not jolt when they land.
    if (feed.loading && feed.messages.isEmpty) {
      return _FeedSkeleton(padding: padding);
    }

    if (feed.messages.isEmpty) {
      return Padding(
        padding: padding,
        child: Center(
          child: Text(
            l10n.t('chat.noChatMessages'),
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      );
    }

    // Newest first, because the list is reversed.
    final items = feed.messages.reversed.toList(growable: false);

    return ListView.builder(
      controller: scroll,
      reverse: true,
      padding: padding,
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
              entry: feed.contents[message.id],
              feed: feed,
              onReply: onReply,
              // A new day always begins a new run, whatever the clock says.
              startsRun: startsDay || !_sameRun(feed, older, message),
              endsRun: !_sameRun(feed, message, newer),
            ),
          ],
        );
      },
    );
  }

  /// Whether [next] continues [previous]'s run: same sender, both ordinary messages, close in time.
  ///
  /// A call event is never part of a run — it is a system aside, not something anybody said.
  ///
  /// SAME SENDER MEANS THE AUTHENTICATED ONE, and that is not a nicety. A run shows its author's
  /// name ONCE, at the top. Group by the envelope's `senderId` and the untrusted server can stamp
  /// one member's id on another member's ciphertext: the two messages join into a single run, the
  /// second renders with no name of its own, and it is read as the first person's words. The name
  /// was never wrong — it simply was not shown, which is worse, because nothing looks off.
  static bool _sameRun(
    MessageFeedState feed,
    ChatMessage? previous,
    ChatMessage? next,
  ) {
    if (previous == null || next == null) return false;
    if (_senderKey(feed, previous) != _senderKey(feed, next)) return false;
    if (previous.contentType == ContentType.callEvent) return false;
    if (next.contentType == ContentType.callEvent) return false;

    final a = DateTime.tryParse(previous.createdAt);
    final b = DateTime.tryParse(next.createdAt);
    if (a == null || b == null) return false;

    return b.difference(a).abs() <= _runGap;
  }

  /// Who a message is grouped under: the MLS-authenticated sender where this device has one, the
  /// envelope otherwise (a message it cannot read has no signature to go on).
  ///
  /// A message whose signature and envelope DISAGREE gets a key of its own, so it can never be
  /// folded into anybody's run — it has to carry its own unverified marker.
  static String _senderKey(MessageFeedState feed, ChatMessage message) {
    final attribution = feed.contents[message.id]?.attribution;
    final view = resolveAuthor(attribution, message.senderId);
    if (view.tampered) return 'unverified:${message.id}';
    return view.userId;
  }
}

/// Bubble-shaped placeholders, alternating sides and widths so the feed looks like a conversation
/// before it is one.
class _FeedSkeleton extends StatelessWidget {
  const _FeedSkeleton({required this.padding});

  final EdgeInsets padding;

  static const _widths = [0.55, 0.4, 0.68, 0.35, 0.5, 0.6];

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final width = MediaQuery.sizeOf(context).width;

    return ListView.builder(
      reverse: true,
      padding: padding,
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
    required this.entry,
    required this.feed,
    required this.onReply,
    required this.startsRun,
    required this.endsRun,
  });

  final ChatMessage message;
  final Conversation conversation;
  final String myUserId;

  /// Null when this device cannot read the message at all.
  /// What this device holds for the message: the content AND how its author was established.
  /// Null when it could not be read at all.
  final CachedEntry? entry;

  ChatContent? get content => entry?.content;

  /// Needed to resolve a reply's quote from a message we already hold.
  final MessageFeedState feed;

  final void Function(ChatMessage) onReply;
  final bool startsRun;
  final bool endsRun;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Nothing at all until this device knows what the message is — see
    // MessageFeedState.isReadyToShow for why a placeholder here is worse than an empty space.
    if (!feed.isReadyToShow(message)) return const SizedBox.shrink();

    // Who MLS says wrote this, and whether the envelope agrees.
    //
    // `isOwn` decides which side of the feed the bubble sits on, and it is answered from the
    // AUTHENTICATED sender for every message this device has read. Left to the envelope only where
    // there is no plaintext — and therefore no signature — to go on, which is a message this device
    // cannot read at all.
    final author = resolveAuthor(entry?.attribution, message.senderId);
    final isOwn = isOwnMessage(entry?.attribution, message.senderId, myUserId);

    // Somebody joined or left: a line the conversation says about itself, centred and quiet, with
    // no bubble and no sender — because nobody sent it.
    final membership = message.membershipEvent;
    if (membership != null) {
      return _MembershipLine(
        event: membership,
        conversation: conversation,
        myUserId: myUserId,
      );
    }

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
    //
    // The name comes from the identity MLS authenticated, never from the envelope. When the two
    // disagree the bubble says so instead (see MessageBubble.senderUnverified): naming either one
    // would be picking a side, and that is exactly the silent misattribution the authenticated
    // sender exists to prevent.
    String? senderName;
    if (conversation.isGroup && !isOwn && !author.tampered) {
      final member = conversation.memberOf(author.userId);
      senderName = member == null ? null : userLabel(member.user);
    }

    final photos = content?.photos ?? const <ChatPhoto>[];

    return MessageBubble(
      body: content?.body,
      createdAt: message.createdAt,
      isOwn: isOwn,
      // Only our own: a tick on someone else's would tell them what they already know.
      receipt: isOwn
          ? messageReceipt(message.seq, feed.members, myUserId)
          : null,
      senderName: senderName,
      senderUnverified: author.tampered,
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
    final quotedEntry = quoted == null ? null : feed.contents[quoted.id];
    final quotedContent = quotedEntry?.content;

    // Named from the AUTHENTICATED sender of the quoted message wherever this device has decrypted
    // it. A quote is a claim about what somebody else said, so attributing it from the envelope —
    // the server's word — would let the server put words in a member's mouth in the one place a
    // reader is least likely to check them against the original. When the two disagree, name
    // nobody: ReplyQuote renders its unknown-author form.
    String? author;
    if (quoted != null) {
      final view = resolveAuthor(quotedEntry?.attribution, quoted.senderId);
      if (!view.tampered) {
        final member = conversation.memberOf(view.userId);
        author = member == null ? null : userLabel(member.user);
      }
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

    final palette = GlassPalette.of(context);

    return GlassComposerBar(
      child: Column(
        children: [
          if (notice != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(10, 6, 10, 8),
              child: Row(
                children: [
                  Icon(
                    Icons.lock_outline,
                    size: 15,
                    color: palette.mutedForeground,
                  ),
                  const SizedBox(width: GlassMetrics.gap),
                  Expanded(
                    child: Text(
                      notice!,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: palette.mutedForeground,
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
              padding: const EdgeInsets.fromLTRB(6, 2, 2, 6),
              child: Row(
                children: [
                  Expanded(
                    child: ReplyQuote(
                      author: replyAuthor,
                      text: replyText,
                      compact: true,
                    ),
                  ),
                  GlassComposerGlyph(
                    icon: Icons.close,
                    semanticLabel: l10n.t('common.cancel'),
                    onPressed: onCancelReply,
                  ),
                ],
              ),
            ),

          if (photos.isNotEmpty)
            SizedBox(
              height: 72,
              child: ListView.separated(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.fromLTRB(4, 2, 4, 8),
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
              GlassComposerGlyph(
                icon: Icons.add_photo_alternate_outlined,
                semanticLabel: l10n.t('chat.attachPhoto'),
                onPressed: sending ? null : onAttach,
              ),
              Expanded(
                child: GlassComposerField(
                  controller: controller,
                  hintText: l10n.t('chat.composerPlaceholder'),
                ),
              ),
              const SizedBox(width: 4),
              GlassSendButton(
                sending: sending,
                enabled: canSend,
                onPressed: onSend,
                semanticLabel: l10n.t('chat.send'),
              ),
            ],
          ),
        ],
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

/// Confirms and clears the conversation's history: purges it server-side (a per-member watermark,
/// so no one else's view is touched) and wipes the local plaintext, keeping the conversation. The
/// open feed is emptied in place so the now-clear chat shows without leaving it. Irreversible — the
/// bodies are gone for good under MLS — so it is always behind a confirm.
Future<void> _confirmClearHistory(
  BuildContext context,
  WidgetRef ref,
  Conversation conversation,
) async {
  final l10n = context.l10n;
  final confirmed = await showAdaptiveConfirm(
    context,
    title: l10n.t('chat.clearHistory'),
    message: l10n.t('chat.clearHistoryConfirm'),
    confirmLabel: l10n.t('chat.clearHistory'),
    cancelLabel: l10n.t('common.cancel'),
    isDestructive: true,
  );
  if (!confirmed || !context.mounted) return;

  try {
    await ref
        .read(conversationListProvider.notifier)
        .clearHistory(conversation.id);
    ref.read(messageFeedProvider(conversation.id).notifier).clearHistory();
    if (context.mounted) notifySuccess(context, l10n.t('chat.historyCleared'));
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
        .read(conversationListProvider.notifier)
        .leave(conversation.id, myUserId);
    if (!context.mounted) return;
    context.go('/');
  } on Object catch (e) {
    if (context.mounted) notifyError(context, l10n.t('chat.deleteFailed'), e);
  }
}

/// The centred line that marks somebody joining or leaving.
class _MembershipLine extends StatelessWidget {
  const _MembershipLine({
    required this.event,
    required this.conversation,
    required this.myUserId,
  });

  final MembershipEvent event;
  final Conversation conversation;
  final String myUserId;

  /// A member's name, or a short stub if they have since left and are no longer on the roster.
  String _name(BuildContext context, String userId) {
    if (userId == myUserId) return context.l10n.t('chat.you');
    final member = conversation.memberOf(userId);
    return member == null ? shortUserLabel(userId) : userLabel(member.user);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final subject = _name(context, event.userId);
    final text = switch (event.action) {
      'added' => l10n.tp('chat.memberAdded', {
        'name': subject,
        'by': _name(context, event.actorId),
      }),
      'removed' => l10n.tp('chat.memberRemoved', {
        'name': subject,
        'by': _name(context, event.actorId),
      }),
      'left' => l10n.tp('chat.memberLeft', {'name': subject}),
      _ => '',
    };
    if (text.isEmpty) return const SizedBox.shrink();

    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
      child: Center(
        child: Text(
          text,
          textAlign: TextAlign.center,
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ),
    );
  }
}
