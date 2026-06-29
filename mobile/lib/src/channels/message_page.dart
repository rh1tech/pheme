import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/format.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../l10n/app_localizations.dart';
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
    return AdaptiveScaffold(
      title: Text(l10n.t('channel.messageView')),
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
    final scheme = Theme.of(context).colorScheme;
    return ListView(
      children: [
        if (message.images.isNotEmpty) MessageCarousel(images: message.images),
        Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                message.title.isEmpty
                    ? l10n.t('channel.noTitle')
                    : message.title,
                style: const TextStyle(
                  fontWeight: FontWeight.w600,
                  fontSize: 18,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                formatDateTime(message.createdAt),
                style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
              ),
              if (message.body.isNotEmpty) ...[
                const SizedBox(height: 12),
                Text(message.body, style: const TextStyle(fontSize: 15)),
              ],
              const SizedBox(height: 24),
              _CommentsSection(
                channelId: widget.channelId,
                messageId: widget.messageId,
                commentsAllowed: message.commentsAllowed,
              ),
            ],
          ),
        ),
      ],
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
  });

  final String channelId;
  final String messageId;
  final bool commentsAllowed;

  @override
  ConsumerState<_CommentsSection> createState() => _CommentsSectionState();
}

class _CommentsSectionState extends ConsumerState<_CommentsSection> {
  final _input = TextEditingController();
  List<Comment> _comments = const [];
  String _nextCursor = '';
  bool _loading = true;
  bool _posting = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
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

  Future<void> _loadMore() async {
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
      children: [
        Text(
          l10n.t('comment.title'),
          style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
        ),
        const SizedBox(height: 12),
        if (widget.commentsAllowed && canComment)
          Row(
            crossAxisAlignment: CrossAxisAlignment.end,
            children: [
              Expanded(
                child: AdaptiveTextField(
                  controller: _input,
                  placeholder: l10n.t('comment.placeholder'),
                  minLines: 1,
                  maxLines: 4,
                  onChanged: (_) => setState(() {}),
                ),
              ),
              const SizedBox(width: 8),
              AdaptiveButton.filled(
                onPressed: (_posting || _input.text.trim().isEmpty)
                    ? null
                    : _post,
                child: _posting
                    ? const AdaptiveProgress(size: 18)
                    : Text(l10n.t('comment.post')),
              ),
            ],
          ),
        if (!widget.commentsAllowed)
          Text(
            l10n.t('comment.closed'),
            style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
          ),
        if (widget.commentsAllowed && !canComment)
          Text(
            l10n.t('comment.joinToComment'),
            style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
          ),
        const SizedBox(height: 12),
        if (_loading)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 16),
            child: Center(child: AdaptiveProgress()),
          )
        else if (_comments.isEmpty)
          Text(
            l10n.t('comment.empty'),
            style: TextStyle(fontSize: 13, color: scheme.onSurfaceVariant),
          )
        else
          ..._comments.map(
            (c) => _CommentTile(
              comment: c,
              canDelete: canModerate || c.userId == myId,
              onDelete: () => _delete(c),
            ),
          ),
        if (_nextCursor.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: AdaptiveButton.text(
              onPressed: _loadMore,
              child: Text(l10n.t('comment.loadMore')),
            ),
          ),
      ],
    );
  }
}

/// A single comment row: author avatar/name, timestamp, body, optional delete.
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
    final scheme = Theme.of(context).colorScheme;
    final label = comment.author.label(l10n.t('comment.anonymous'));
    final avatarId = comment.author.avatarId;
    final avatarUrl = (avatarId != null && avatarId.isNotEmpty)
        ? ref.read(repositoryProvider).imageUrl(avatarId)
        : null;
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor: scheme.primaryContainer,
            foregroundImage: avatarUrl != null ? NetworkImage(avatarUrl) : null,
            child: Text(
              label.characters.take(2).toString().toUpperCase(),
              style: const TextStyle(fontSize: 12),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        label,
                        style: const TextStyle(
                          fontWeight: FontWeight.w600,
                          fontSize: 14,
                        ),
                      ),
                    ),
                    Text(
                      formatDateTime(comment.createdAt),
                      style: TextStyle(
                        fontSize: 11,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                    if (canDelete)
                      GestureDetector(
                        onTap: onDelete,
                        behavior: HitTestBehavior.opaque,
                        child: Padding(
                          padding: const EdgeInsets.only(left: 8),
                          child: Icon(
                            Icons.delete_outline,
                            size: 18,
                            color: scheme.error,
                          ),
                        ),
                      ),
                  ],
                ),
                const SizedBox(height: 2),
                Text(comment.body, style: const TextStyle(fontSize: 14)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
