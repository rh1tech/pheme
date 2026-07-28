import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/format.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../chat/chat_time.dart';
import '../chat/widgets/chat_wallpaper.dart';
import '../chat/widgets/call_event_bubble.dart';
import '../l10n/app_localizations.dart';
import '../widgets/glass/glass.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/message_carousel.dart';

/// Full message view: all images in a carousel (Instagram-style) followed by the
/// title, timestamp and body. Reached by tapping a message or a notification.
class MessagePage extends ConsumerStatefulWidget {
  const MessagePage({
    super.key,
    required this.channelId,
    required this.messageId,
  });

  final String channelId;
  final String messageId;

  @override
  ConsumerState<MessagePage> createState() => _MessagePageState();
}

class _MessagePageState extends ConsumerState<MessagePage> {
  Message? _message;
  bool _loading = true;
  bool _error = false;

  /// Whether the post is shown in full. Collapsed it is capped at 30% of the screen, so the
  /// comments below it are visible without scrolling past the whole broadcast first.
  bool _expanded = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = false;
    });
    try {
      final message = await ref
          .read(repositoryProvider)
          .getMessage(widget.channelId, widget.messageId);
      if (!mounted) return;
      setState(() {
        _message = message;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = true;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    // The bar carries the post's own title. It used to read "Message" while the title was
    // repeated as the first line of the body, so the top of the screen said nothing and the same
    // words appeared twice.
    final title = _message?.title ?? '';
    return AdaptiveScaffold(
      title: Text(
        title.isEmpty ? l10n.t('channel.messageView') : title,
        overflow: TextOverflow.ellipsis,
      ),
      body: _body(context, l10n),
    );
  }

  Widget _body(BuildContext context, AppLocalizations l10n) {
    if (_loading) {
      return const Center(child: AdaptiveProgress());
    }
    final message = _message;
    if (_error || message == null) {
      return ErrorView(
        message: l10n.t('channel.messageNotFound'),
        onRetry: _load,
      );
    }
    // The post on top, the comments filling what is left, the composer pinned to the bottom — the
    // proportions the request asked for and the shape a chat has: a thing to read, a conversation
    // about it, and a place to write.
    //
    // The post is capped at 30% of the screen and opens on a tap. A long broadcast used to push
    // the comments off the bottom entirely, so a post with fifty replies looked like a post with
    // none until you scrolled past the whole of it.
    return Column(
      // Stretch, not the default centre: the post sizes to its content, so a centring parent put
      // the date and the text in the middle of the screen however left-aligned they were inside.
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Open, the post takes most of the screen and scrolls within it; closed, it sizes to its
        // own (capped) content. A Flexible child cannot scroll against an unbounded height, which
        // is why this is not simply always intrinsic.
        if (_expanded)
          Expanded(
            child: _PostBody(
              message: message,
              expanded: true,
              onToggle: () => setState(() => _expanded = false),
            ),
          )
        else
          // Bounds the collapsed post ENTIRELY — pictures, date, text and control together. Capping
          // only the text left a post with a photograph taking half the screen, which is the space
          // the comments were supposed to have.
          ConstrainedBox(
            constraints: BoxConstraints(
              maxHeight: MediaQuery.sizeOf(context).height * 0.26,
            ),
            child: SingleChildScrollView(
              child: _PostBody(
                message: message,
                expanded: false,
                onToggle: () => setState(() => _expanded = true),
              ),
            ),
          ),
        const SizedBox(height: 8),
        // Open, the comments give up their space entirely — the post takes everything down to the
        // field you would reply in, which is what "full screen" meant. The field itself stays,
        // because losing the ability to reply while reading is not part of that.
        if (_expanded)
          _CommentsSection(
            channelId: widget.channelId,
            messageId: widget.messageId,
            commentsAllowed: message.commentsAllowed,
            showList: false,
          )
        else
          Expanded(
            child: _CommentsSection(
              channelId: widget.channelId,
              messageId: widget.messageId,
              commentsAllowed: message.commentsAllowed,
            ),
          ),
      ],
    );
  }
}

/// The post itself: its pictures, when it was sent, and what it says.
///
/// Only the TEXT collapses, and only when there is enough of it to be worth collapsing. The first
/// version showed "Show more" under every post — including one-line ones, where there was nothing
/// more to show — and expanding re-laid-out the whole column, which read as the message jumping.
///
/// Whether it overflows is measured rather than guessed: the same text, style and width are laid
/// out with a TextPainter and compared against the cap. A character-count heuristic would be wrong
/// for one long word and wrong again for a narrow screen.
class _PostBody extends StatelessWidget {
  const _PostBody({
    required this.message,
    required this.expanded,
    required this.onToggle,
  });

  final Message message;
  final bool expanded;
  final VoidCallback onToggle;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    const bodyStyle = TextStyle(fontSize: 15);

    // What the post actually says: its body, or its title when the body is empty.
    final text = message.body.isNotEmpty ? message.body : message.title;

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: expanded ? MainAxisSize.max : MainAxisSize.min,
        children: [
          if (message.images.isNotEmpty) ...[
            MessageCarousel(images: message.images),
            const SizedBox(height: 12),
          ],
          Text(
            formatDateTime(message.createdAt),
            style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
          ),
          // The composer makes the first line the title and the rest the body, so a one-line post
          // has a title and NOTHING else. Its only copy of the text was then the app bar, which
          // truncates — the message simply did not appear on its own screen. Falling back to the
          // title here means the text is always somewhere it can be read in full.
          if (text.isNotEmpty) ...[
            const SizedBox(height: 10),
            // Expanded when open so the scrollable inside is bounded; intrinsic when closed so the
            // post takes only the room its clipped text needs.
            if (expanded)
              Expanded(child: _bodyBlock(context, text, bodyStyle, scheme))
            else
              _bodyBlock(context, text, bodyStyle, scheme),
          ],
        ],
      ),
    );
  }

  Widget _bodyBlock(
    BuildContext context,
    String text,
    TextStyle bodyStyle,
    ColorScheme scheme,
  ) {
    final l10n = context.l10n;
    final cap = MediaQuery.sizeOf(context).height * 0.16;
    return LayoutBuilder(
      builder: (context, constraints) {
        final painter = TextPainter(
          text: TextSpan(text: text, style: bodyStyle),
          textDirection: Directionality.of(context),
        )..layout(maxWidth: constraints.maxWidth);
        final overflows = painter.height > cap;

        final rendered = Text(text, style: bodyStyle);
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: expanded ? MainAxisSize.max : MainAxisSize.min,
          children: [
            if (expanded)
              // Scrolls against the height the page granted, so a post longer than the
              // screen can be read to the end — and "Show less" below stays put rather
              // than sitting somewhere off the bottom of it.
              Flexible(child: SingleChildScrollView(child: rendered))
            else if (!overflows)
              rendered
            else
              // Clipped rather than scrolled: a scrollable box inside a scrollable page
              // fights the reader for the gesture.
              ClipRect(
                child: SizedBox(
                  height: cap,
                  width: double.infinity,
                  child: Align(alignment: Alignment.topLeft, child: rendered),
                ),
              ),
            // Offered only when there is genuinely more to see, and it turns back into
            // "Show less" once open — a control that only ever expands leaves the reader
            // with no way back.
            if (overflows)
              TextButton(
                onPressed: onToggle,
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.only(top: 14, bottom: 4),
                  minimumSize: Size.zero,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      l10n.t(
                        expanded ? 'channel.showLess' : 'channel.showMore',
                      ),
                      style: TextStyle(fontSize: 12, color: scheme.primary),
                    ),
                    Icon(
                      expanded ? Icons.expand_less : Icons.expand_more,
                      size: 16,
                      color: scheme.primary,
                    ),
                  ],
                ),
              ),
          ],
        );
      },
    );
  }
}

/// The comments list and composer for a message. Active members (and the owner)
/// can post; the author or a channel owner/admin can delete.
class _CommentsSection extends ConsumerStatefulWidget {
  const _CommentsSection({
    required this.channelId,
    required this.messageId,
    required this.commentsAllowed,
    this.showList = true,
  });

  final String channelId;
  final String messageId;
  final bool commentsAllowed;

  /// Whether the thread is shown. False while the post is open full-screen, where the composer is
  /// all that remains of this section.
  final bool showList;

  @override
  ConsumerState<_CommentsSection> createState() => _CommentsSectionState();
}

class _CommentsSectionState extends ConsumerState<_CommentsSection> {
  final _input = TextEditingController();
  List<Comment> _comments = const [];
  String _nextCursor = '';

  /// Guards the automatic load. Without it a fast flick to the top fires several loads for the
  /// same cursor before the first returns, and the thread gains each page two or three times.
  bool _loadingMore = false;

  final _scroll = ScrollController();
  bool _loading = true;
  bool _posting = false;

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_onScroll);
    _load();
  }

  @override
  void dispose() {
    _scroll.dispose();
    _input.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final page = await ref
          .read(repositoryProvider)
          .listComments(widget.channelId, widget.messageId);
      if (!mounted) return;
      setState(() {
        _comments = page.comments;
        _nextCursor = page.nextCursor;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _loading = false);
      notifyError(context, context.l10n.t('comment.loadFailed'), e);
    }
  }

  /// Older comments load on reaching the top, which in a reversed list is the far end.
  ///
  /// 300px of warning rather than the exact edge, so the next page is usually there before the
  /// reader arrives and the thread does not visibly stall.
  void _onScroll() {
    if (!_scroll.hasClients || _loadingMore || _nextCursor.isEmpty) return;
    final pos = _scroll.position;
    if (pos.pixels >= pos.maxScrollExtent - 300) _loadMore();
  }

  Future<void> _loadMore() async {
    if (_loadingMore || _nextCursor.isEmpty) return;
    setState(() => _loadingMore = true);
    try {
      final page = await ref
          .read(repositoryProvider)
          .listComments(
            widget.channelId,
            widget.messageId,
            cursor: _nextCursor,
          );
      if (!mounted) return;
      setState(() {
        _comments = [..._comments, ...page.comments];
        _nextCursor = page.nextCursor;
        _loadingMore = false;
      });
    } catch (e) {
      if (mounted) {
        notifyError(context, context.l10n.t('comment.loadFailed'), e);
      }
    }
  }

  Future<void> _post() async {
    final text = _input.text.trim();
    if (text.isEmpty) return;
    FocusScope.of(context).unfocus();
    setState(() => _posting = true);
    final l10n = context.l10n;
    try {
      final created = await ref
          .read(repositoryProvider)
          .postComment(widget.channelId, widget.messageId, text);
      if (!mounted) return;
      setState(() {
        _comments = [created, ..._comments];
        _input.clear();
      });
      notifySuccess(context, l10n.t('comment.posted'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('comment.postFailed'), e);
    } finally {
      if (mounted) setState(() => _posting = false);
    }
  }

  Future<void> _delete(Comment c) async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('comment.delete'),
      message: l10n.t('comment.deleteConfirm'),
      confirmLabel: l10n.t('common.delete'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;
    try {
      await ref
          .read(repositoryProvider)
          .deleteComment(widget.channelId, widget.messageId, c.id);
      if (!mounted) return;
      setState(() => _comments = _comments.where((x) => x.id != c.id).toList());
      notifySuccess(context, l10n.t('comment.deleted'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('comment.deleteFailed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    final relation = ref
        .watch(channelRelationProvider(widget.channelId))
        .asData
        ?.value;
    final canComment = relation?.status == MembershipStatus.active;
    final canModerate = relation?.canManage ?? false;
    final myId = ref.watch(authControllerProvider).userId;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: widget.showList ? MainAxisSize.max : MainAxisSize.min,
      children: [
        // The comments scroll on their own, so the composer below stays put — the same
        // arrangement a chat has. They used to be part of one long page scroll with the post, so
        // the field you type into drifted off the bottom as the thread grew.
        if (widget.showList)
          Expanded(
            child: ChatWallpaper(
              child: _loading
                  ? const Center(child: AdaptiveProgress())
                  : _comments.isEmpty
                  // "No comments yet. Be the first to comment." under a post that does not accept
                  // comments invites the reader to do something the screen will refuse.
                  ? (widget.commentsAllowed
                        ? Center(
                            child: Padding(
                              padding: const EdgeInsets.all(16),
                              child: Text(
                                l10n.t('comment.empty'),
                                style: TextStyle(
                                  fontSize: 13,
                                  color: scheme.onSurfaceVariant,
                                ),
                              ),
                            ),
                          )
                        : const SizedBox.shrink())
                  : ListView.builder(
                      // Reversed, like the chat and channel feeds. The server returns comments
                      // newest-first, so a plain list put the newest at the TOP and opening a thread
                      // showed its end. Reversed, index 0 draws at the bottom and the view starts
                      // there — on the latest comment, with no scrolling to get to it.
                      reverse: true,
                      controller: _scroll,
                      padding: const EdgeInsets.fromLTRB(12, 4, 12, 8),
                      itemCount:
                          _comments.length + (_nextCursor.isNotEmpty ? 1 : 0),
                      itemBuilder: (context, i) {
                        // The last index draws at the TOP of a reversed list, which is where older
                        // comments belong.
                        if (i >= _comments.length) {
                          // A spinner rather than a button: older comments now arrive on their own
                          // when the reader reaches the top, so this reports that they are coming
                          // rather than asking permission to fetch them.
                          return const Padding(
                            padding: EdgeInsets.symmetric(vertical: 12),
                            child: Center(child: AdaptiveProgress(size: 20)),
                          );
                        }
                        final c = _comments[i];
                        // The same day badge the chat feed draws. Reversed list, so the comment
                        // ABOVE this one on screen is the older one at index+1, and the badge
                        // belongs above the first comment of each day.
                        final older = i + 1 < _comments.length
                            ? _comments[i + 1]
                            : null;
                        final day = messageDay(c.createdAt);
                        final olderDay = older == null
                            ? null
                            : messageDay(older.createdAt);
                        final startsDay =
                            day != null &&
                            (olderDay == null || day != olderDay);
                        return Column(
                          children: [
                            if (startsDay) DateSeparator(day: day),
                            _CommentTile(
                              comment: c,
                              canDelete: canModerate || c.userId == myId,
                              onDelete: () => _delete(c),
                            ),
                          ],
                        );
                      },
                    ),
            ),
          ),
        // The same bar the channel composes a post with — rounded field, filled send button, and
        // the same rule along its top. A labelled "Comment" button beside a plain box was a form;
        // this is a place to say something.
        if (widget.commentsAllowed && canComment)
          Container(
            decoration: BoxDecoration(
              color: scheme.surface,
              border: Border(
                top: BorderSide(
                  color: Theme.of(context).dividerColor.withValues(alpha: 0.5),
                ),
              ),
            ),
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: TextField(
                    controller: _input,
                    minLines: 1,
                    maxLines: 4,
                    textCapitalization: TextCapitalization.sentences,
                    onChanged: (_) => setState(() {}),
                    decoration: InputDecoration(
                      hintText: l10n.t('comment.placeholder'),
                      border: OutlineInputBorder(
                        borderRadius: BorderRadius.circular(20),
                      ),
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 14,
                        vertical: 10,
                      ),
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                IconButton.filled(
                  // Explicit colours. The filled default resolves to primary on primaryContainer,
                  // which on this palette is a violet arrow on a violet disc — the button was there
                  // and could not be read.
                  style: IconButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.primary,
                    foregroundColor: Theme.of(context).colorScheme.onPrimary,
                    disabledBackgroundColor: Theme.of(
                      context,
                    ).colorScheme.surfaceContainerHighest,
                    disabledForegroundColor: Theme.of(
                      context,
                    ).colorScheme.onSurfaceVariant,
                  ),
                  icon: _posting
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.send),
                  tooltip: l10n.t('comment.post'),
                  onPressed: (_posting || _input.text.trim().isEmpty)
                      ? null
                      : _post,
                ),
              ],
            ),
          ),
        if (!widget.commentsAllowed)
          Padding(
            padding: const EdgeInsets.all(16),
            child: Center(
              child: Text(
                l10n.t('comment.closed'),
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
              ),
            ),
          ),
        if (widget.commentsAllowed && !canComment)
          Text(
            l10n.t('comment.joinToComment'),
            style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
          ),
        const SizedBox(height: 12),
      ],
    );
  }
}

/// A single comment row: author avatar/name, timestamp, body, optional delete.
/// One comment, as a bubble.
///
/// Comments were a flat list of avatar-plus-name-plus-text rows — a comment section under an
/// article, which is what the channel screen used to be. Now that a channel reads as a
/// conversation, its comments should too: they are the one place in a channel where more than one
/// person speaks, and a bubble is what tells them apart at a glance.
///
/// Everyone's comment is left-aligned. Distinguishing your own from another's would need the
/// reader's user id, which this widget has no reason to know, and the author's name above the text
/// already says who wrote it.
class _CommentTile extends ConsumerWidget {
  const _CommentTile({
    required this.comment,
    required this.canDelete,
    required this.onDelete,
  });

  final Comment comment;
  final bool canDelete;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final bubble = BubbleStyle.background(context);

    final label = comment.author.label(l10n.t('comment.anonymous'));
    final avatarId = comment.author.avatarId;
    final avatarUrl = (avatarId != null && avatarId.isNotEmpty)
        ? ref.read(repositoryProvider).imageUrl(avatarId)
        : null;

    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          CircleAvatar(
            radius: 14,
            backgroundColor: scheme.primaryContainer,
            foregroundImage: avatarUrl != null ? NetworkImage(avatarUrl) : null,
            child: Text(
              label.characters.take(2).toString().toUpperCase(),
              style: const TextStyle(fontSize: 11),
            ),
          ),
          const SizedBox(width: 8),
          Flexible(
            child: Container(
              constraints: BoxConstraints(
                maxWidth: MediaQuery.sizeOf(context).width * 0.72,
              ),
              decoration: BoxDecoration(
                color: bubble,
                borderRadius: const BorderRadius.only(
                  topLeft: Radius.circular(4),
                  topRight: Radius.circular(16),
                  bottomLeft: Radius.circular(16),
                  bottomRight: Radius.circular(16),
                ),
              ),
              padding: const EdgeInsets.fromLTRB(12, 8, 12, 6),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    label,
                    style: TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 13,
                      color: scheme.primary,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(comment.body, style: const TextStyle(fontSize: 14)),
                  const SizedBox(height: 2),
                  Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Spacer(),
                      Text(
                        bubbleTime(l10n, comment.createdAt),
                        style: TextStyle(
                          fontSize: 11,
                          color: scheme.onSurfaceVariant,
                        ),
                      ),
                      // A menu, not a mystery. This was a bare "..." with a long press behind
                      // it: the glyph named no action and the gesture was invisible, so the only
                      // way to discover deleting was to try holding the thing you wanted gone.
                      if (canDelete) ...[
                        const SizedBox(width: 2),
                        GlassMenuAnchor(
                          semanticLabel: l10n.t('comment.menu'),
                          actions: [
                            GlassMenuAction(
                              label: l10n.t('common.delete'),
                              icon: Icons.delete_outline,
                              destructive: true,
                              onSelected: onDelete,
                            ),
                          ],
                          child: SizedBox(
                            height: 18,
                            width: 22,
                            child: Icon(
                              Icons.more_horiz,
                              size: 15,
                              color: scheme.onSurfaceVariant,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}
