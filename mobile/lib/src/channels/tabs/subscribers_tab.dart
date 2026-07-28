import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/adaptive/adaptive.dart';
import '../../widgets/error_view.dart';

/// Per-member action presented in the adaptive action sheet.
enum _MemberAction { makeAdmin, makeUser, ban, unban, remove }

/// Owner/admin view of a channel's subscribers: a pending-approval queue with
/// Approve/Deny actions, and a paginated subscriber list with per-row role and
/// ban controls.
class SubscribersTab extends ConsumerStatefulWidget {
  const SubscribersTab({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<SubscribersTab> createState() => _SubscribersTabState();
}

class _SubscribersTabState extends ConsumerState<SubscribersTab> {
  static const _pageSize = 20;

  List<ChannelMember> _approvals = const [];
  List<ChannelMember> _members = const [];
  int _total = 0;
  bool _loading = true;
  bool _error = false;
  bool _loadingMore = false;
  String? _busyUserId;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load({bool silent = false}) async {
    if (!silent) {
      setState(() {
        _loading = true;
        _error = false;
      });
    }
    try {
      final repo = ref.read(repositoryProvider);
      final approvals = await repo.listApprovals(widget.channelId);
      final page = await repo.listMembers(
        widget.channelId,
        offset: 0,
        limit: _pageSize,
      );
      if (!mounted) return;
      setState(() {
        _approvals = approvals;
        _members = page.items;
        _total = page.total;
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = true;
      });
    }
  }

  Future<void> _loadMore() async {
    setState(() => _loadingMore = true);
    try {
      final page = await ref
          .read(repositoryProvider)
          .listMembers(
            widget.channelId,
            offset: _members.length,
            limit: _pageSize,
          );
      if (!mounted) return;
      setState(() {
        _members = [..._members, ...page.items];
        _total = page.total;
        _loadingMore = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _loadingMore = false);
      notifyError(context, context.l10n.t('channels.loadFailed'), e);
    }
  }

  Future<void> _run(
    String userId,
    Future<void> Function() action, {
    required String successKey,
    required String failKey,
  }) async {
    setState(() => _busyUserId = userId);
    final l10n = context.l10n;
    try {
      await action();
      await _load(silent: true);
      if (mounted) notifySuccess(context, l10n.t(successKey));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t(failKey), e);
    } finally {
      if (mounted) setState(() => _busyUserId = null);
    }
  }

  void _approve(ChannelMember m) => _run(
    m.userId,
    () =>
        ref.read(repositoryProvider).approveMember(widget.channelId, m.userId),
    successKey: 'channel.memberApproved',
    failKey: 'channel.approveFailed',
  );

  void _deny(ChannelMember m) => _run(
    m.userId,
    () => ref.read(repositoryProvider).denyMember(widget.channelId, m.userId),
    successKey: 'channel.memberDenied',
    failKey: 'channel.denyFailed',
  );

  Future<void> _onAction(ChannelMember m, _MemberAction action) async {
    final l10n = context.l10n;
    final repo = ref.read(repositoryProvider);
    switch (action) {
      case _MemberAction.makeAdmin:
        await _run(
          m.userId,
          () => repo.updateMember(
            widget.channelId,
            m.userId,
            role: ChannelRole.admin,
          ),
          successKey: 'channel.memberUpdated',
          failKey: 'channel.memberUpdateFailed',
        );
      case _MemberAction.makeUser:
        await _run(
          m.userId,
          () => repo.updateMember(
            widget.channelId,
            m.userId,
            role: ChannelRole.user,
          ),
          successKey: 'channel.memberUpdated',
          failKey: 'channel.memberUpdateFailed',
        );
      case _MemberAction.unban:
        await _run(
          m.userId,
          () => repo.updateMember(
            widget.channelId,
            m.userId,
            status: MemberStatus.active,
          ),
          successKey: 'channel.memberUpdated',
          failKey: 'channel.memberUpdateFailed',
        );
      case _MemberAction.ban:
        final ok = await showAdaptiveConfirm(
          context,
          title: l10n.t('channel.ban'),
          message: l10n.tp('channel.banConfirm', {'name': m.label}),
          confirmLabel: l10n.t('channel.ban'),
          cancelLabel: l10n.t('common.cancel'),
          isDestructive: true,
        );
        if (!ok) return;
        await _run(
          m.userId,
          () => repo.updateMember(
            widget.channelId,
            m.userId,
            status: MemberStatus.blocked,
          ),
          successKey: 'channel.memberUpdated',
          failKey: 'channel.memberUpdateFailed',
        );
      case _MemberAction.remove:
        final ok = await showAdaptiveConfirm(
          context,
          title: l10n.t('channel.removeMember'),
          message: l10n.tp('channel.removeMemberConfirm', {'name': m.label}),
          confirmLabel: l10n.t('channel.removeMember'),
          cancelLabel: l10n.t('common.cancel'),
          isDestructive: true,
        );
        if (!ok) return;
        await _run(
          m.userId,
          () => repo.removeMember(widget.channelId, m.userId),
          successKey: 'channel.memberRemoved',
          failKey: 'channel.memberRemoveFailed',
        );
    }
  }

  Future<void> _showMemberActions(ChannelMember m) async {
    final action = await _pickMemberAction(m);
    if (action == null || !mounted) return;
    await _onAction(m, action);
  }

  Future<_MemberAction?> _pickMemberAction(ChannelMember m) {
    final l10n = context.l10n;
    final isAdmin = m.role == ChannelRole.admin;
    final isBlocked = m.status == MemberStatus.blocked;
    final roleAction = isAdmin
        ? _MemberAction.makeUser
        : _MemberAction.makeAdmin;
    final roleLabel = l10n.t(
      isAdmin ? 'channel.makeUser' : 'channel.makeAdmin',
    );
    final banAction = isBlocked ? _MemberAction.unban : _MemberAction.ban;
    final banLabel = l10n.t(isBlocked ? 'channel.unban' : 'channel.ban');

    if (isCupertino(context)) {
      return showCupertinoModalPopup<_MemberAction>(
        context: context,
        builder: (ctx) => CupertinoActionSheet(
          title: Text(m.label),
          actions: [
            CupertinoActionSheetAction(
              onPressed: () => Navigator.of(ctx).pop(roleAction),
              child: Text(roleLabel),
            ),
            CupertinoActionSheetAction(
              isDestructiveAction: !isBlocked,
              onPressed: () => Navigator.of(ctx).pop(banAction),
              child: Text(banLabel),
            ),
            CupertinoActionSheetAction(
              isDestructiveAction: true,
              onPressed: () => Navigator.of(ctx).pop(_MemberAction.remove),
              child: Text(l10n.t('channel.removeMember')),
            ),
          ],
          cancelButton: CupertinoActionSheetAction(
            onPressed: () => Navigator.of(ctx).pop(),
            child: Text(l10n.t('common.cancel')),
          ),
        ),
      );
    }

    final scheme = Theme.of(context).colorScheme;
    return showModalBottomSheet<_MemberAction>(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(
                m.label,
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
            ),
            const Divider(height: 1),
            ListTile(
              leading: Icon(
                isAdmin ? Icons.person_outline : Icons.shield_outlined,
              ),
              title: Text(roleLabel),
              onTap: () => Navigator.of(ctx).pop(roleAction),
            ),
            ListTile(
              leading: Icon(
                isBlocked ? Icons.lock_open_outlined : Icons.block,
                color: isBlocked ? null : scheme.error,
              ),
              title: Text(banLabel),
              onTap: () => Navigator.of(ctx).pop(banAction),
            ),
            ListTile(
              leading: Icon(Icons.delete_outline, color: scheme.error),
              title: Text(l10n.t('channel.removeMember')),
              onTap: () => Navigator.of(ctx).pop(_MemberAction.remove),
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    if (_loading) {
      return const Center(child: AdaptiveProgress());
    }
    if (_error) {
      return ErrorView(message: l10n.t('channels.loadFailed'), onRetry: _load);
    }

    final scheme = Theme.of(context).colorScheme;
    final canLoadMore = _members.length < _total;

    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
      children: [
        _SectionHeader(label: l10n.t('channel.pendingApprovals')),
        const SizedBox(height: 8),
        if (_approvals.isEmpty)
          Text(
            l10n.t('channel.noPendingApprovals'),
            style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
          )
        else
          for (final m in _approvals) ...[
            _ApprovalCard(
              member: m,
              busy: _busyUserId == m.userId,
              onApprove: () => _approve(m),
              onDeny: () => _deny(m),
            ),
            const SizedBox(height: 8),
          ],
        const SizedBox(height: 16),
        _SectionHeader(label: l10n.t('channel.tabSubscribers')),
        const SizedBox(height: 8),
        if (_members.isEmpty)
          Text(
            l10n.t('channel.noSubscribers'),
            style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
          )
        else
          for (final m in _members) ...[
            _MemberCard(
              member: m,
              busy: _busyUserId == m.userId,
              onTap: () => _showMemberActions(m),
            ),
            const SizedBox(height: 8),
          ],
        if (canLoadMore) ...[
          const SizedBox(height: 8),
          Center(
            child: AdaptiveButton.text(
              onPressed: _loadingMore ? null : _loadMore,
              child: _loadingMore
                  ? const AdaptiveProgress(size: 18)
                  : Text(l10n.t('channel.loadMore')),
            ),
          ),
        ],
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Text(
      label,
      style: TextStyle(
        fontWeight: FontWeight.w600,
        fontSize: 13,
        color: Theme.of(context).colorScheme.onSurfaceVariant,
        letterSpacing: 0.3,
      ),
    );
  }
}

class _ApprovalCard extends StatelessWidget {
  const _ApprovalCard({
    required this.member,
    required this.busy,
    required this.onApprove,
    required this.onDeny,
  });

  final ChannelMember member;
  final bool busy;
  final VoidCallback onApprove;
  final VoidCallback onDeny;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveCard(
      padding: const EdgeInsets.all(12),
      child: Row(
        children: [
          Expanded(
            child: Text(
              member.label,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w500),
            ),
          ),
          if (busy)
            const Padding(
              padding: EdgeInsets.symmetric(horizontal: 8),
              child: AdaptiveProgress(size: 18),
            )
          else ...[
            AdaptiveButton.text(
              isDestructive: true,
              onPressed: onDeny,
              child: Text(l10n.t('channel.deny')),
            ),
            const SizedBox(width: 4),
            AdaptiveButton.filled(
              onPressed: onApprove,
              child: Text(l10n.t('channel.approve')),
            ),
          ],
        ],
      ),
    );
  }
}

class _MemberCard extends StatelessWidget {
  const _MemberCard({
    required this.member,
    required this.busy,
    required this.onTap,
  });

  final ChannelMember member;
  final bool busy;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    return AdaptiveCard(
      padding: EdgeInsets.zero,
      child: ListTile(
        contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 4),
        title: Text(
          member.label,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(fontWeight: FontWeight.w500),
        ),
        subtitle: Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Wrap(
            spacing: 6,
            children: [
              if (member.role == ChannelRole.admin)
                _Tag(label: l10n.t('channel.roleAdmin'), color: scheme.primary),
              _Tag(
                label: _statusLabel(l10n, member.status),
                color: _statusColor(scheme, member.status),
              ),
            ],
          ),
        ),
        trailing: busy
            ? const AdaptiveProgress(size: 18)
            : Icon(
                isCupertino(context)
                    ? CupertinoIcons.ellipsis
                    : Icons.more_vert,
                color: scheme.onSurfaceVariant,
              ),
        onTap: busy ? null : onTap,
      ),
    );
  }

  static String _statusLabel(AppLocalizations l10n, MemberStatus status) {
    switch (status) {
      case MemberStatus.active:
        return l10n.t('channel.statusActive');
      case MemberStatus.pending:
        return l10n.t('channel.statusPending');
      case MemberStatus.blocked:
        return l10n.t('channel.statusBlocked');
    }
  }

  static Color _statusColor(ColorScheme scheme, MemberStatus status) {
    switch (status) {
      case MemberStatus.active:
        return scheme.tertiary;
      case MemberStatus.pending:
        return scheme.secondary;
      case MemberStatus.blocked:
        return scheme.error;
    }
  }
}

class _Tag extends StatelessWidget {
  const _Tag({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
