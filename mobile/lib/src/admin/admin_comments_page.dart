import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import 'admin_models.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// Comment moderation: every comment on the server, newest first, with the two things a moderator
/// does about one — delete it, or block whoever wrote it.
class AdminCommentsPage extends ConsumerWidget {
  const AdminCommentsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final admin = ref.read(adminRepositoryProvider);

    return AdminListScreen<AdminComment>(
      title: l10n.t('admin.navComments'),
      searchPlaceholder: l10n.t('admin.searchComments'),
      emptyLabel: l10n.t('admin.noComments'),
      fetch: (query, page) =>
          admin.listComments(query: query, page: page, limit: adminPageLimit),
      rowBuilder: (context, comment, reload) =>
          _CommentRow(comment: comment, onChanged: reload),
    );
  }
}

class _CommentRow extends ConsumerStatefulWidget {
  const _CommentRow({required this.comment, required this.onChanged});

  final AdminComment comment;
  final VoidCallback onChanged;

  @override
  ConsumerState<_CommentRow> createState() => _CommentRowState();
}

class _CommentRowState extends ConsumerState<_CommentRow> {
  bool _busy = false;

  AdminComment get _comment => widget.comment;

  Future<void> _run(
    Future<void> Function() action, {
    required String successKey,
    required String failKey,
    bool reload = true,
  }) async {
    setState(() => _busy = true);
    final l10n = context.l10n;
    try {
      await action();
      if (!mounted) return;
      notifySuccess(context, l10n.t(successKey));
      if (reload) widget.onChanged();
    } on Object catch (e) {
      if (!mounted) return;
      notifyError(context, l10n.t(failKey), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete() async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('common.delete'),
      message: l10n
          .t('admin.deleteCommentConfirm')
          .replaceAll('{name}', _authorLabel(l10n)),
      confirmLabel: l10n.t('common.delete'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;
    await _run(
      () => ref.read(adminRepositoryProvider).deleteComment(_comment.id),
      successKey: 'admin.commentDeleted',
      failKey: 'admin.deleteFailed',
    );
  }

  /// Banning the author is a change to the USER, not to this comment — so the list is left alone.
  /// Reloading here would be misleading: the comment is still there, and a row that vanished would
  /// suggest banning had deleted it.
  Future<void> _banAuthor() async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('admin.banAuthor'),
      message: l10n
          .t('admin.banAuthorConfirm')
          .replaceAll('{name}', _authorLabel(l10n)),
      confirmLabel: l10n.t('admin.banAuthor'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;
    await _run(
      () => ref
          .read(adminRepositoryProvider)
          .updateUser(_comment.authorId, status: 'blocked'),
      successKey: 'admin.authorBanned',
      failKey: 'admin.updateFailed',
      reload: false,
    );
  }

  /// The author's email, or a neutral word when the account behind it is gone.
  String _authorLabel(AppLocalizations l10n) => _comment.authorEmail.isEmpty
      ? l10n.t('admin.anonymousAuthor')
      : _comment.authorEmail;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);

    return ListTile(
      isThreeLine: true,
      title: Text(_comment.body, maxLines: 3, overflow: TextOverflow.ellipsis),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 2,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(_authorLabel(l10n), style: theme.textTheme.bodySmall),
            if (_comment.channelName.isNotEmpty)
              Text(
                '· ${_comment.channelName}',
                style: theme.textTheme.bodySmall,
              ),
            if (_comment.messageTitle.isNotEmpty)
              Text(
                '· ${_comment.messageTitle}',
                style: theme.textTheme.bodySmall,
              ),
            Text(
              adminDate(_comment.createdAt),
              style: theme.textTheme.bodySmall,
            ),
          ],
        ),
      ),
      trailing: _busy
          ? const SizedBox.square(
              dimension: GlassMetrics.minTapTarget,
              child: Center(child: AdaptiveProgress(size: 18)),
            )
          : GlassMenuButton(
              semanticLabel: l10n.t('admin.actions'),
              actions: [
                // Only when there is an account left to ban. A comment whose author has been
                // deleted still needs moderating, and offering an action that would 404 is worse
                // than not offering it.
                if (_comment.authorId.isNotEmpty)
                  GlassMenuAction(
                    label: l10n.t('admin.banAuthor'),
                    icon: Icons.block,
                    destructive: true,
                    onSelected: _banAuthor,
                  ),
                GlassMenuAction(
                  label: l10n.t('common.delete'),
                  icon: Icons.delete_outline,
                  destructive: true,
                  onSelected: _delete,
                ),
              ],
            ),
    );
  }
}
