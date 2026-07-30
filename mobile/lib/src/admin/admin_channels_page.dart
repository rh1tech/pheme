import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import 'admin_models.dart';
import 'admin_providers.dart';
import 'widgets/admin_ui.dart';

/// Every channel on the server: search, disable, delete, or open one to read what it has carried.
class AdminChannelsPage extends ConsumerWidget {
  const AdminChannelsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final admin = ref.read(adminRepositoryProvider);

    return AdminListScreen<AdminChannel>(
      title: l10n.t('admin.navChannels'),
      searchPlaceholder: l10n.t('admin.searchChannels'),
      emptyLabel: l10n.t('admin.noChannels'),
      fetch: (query, page) =>
          admin.listChannels(query: query, page: page, limit: adminPageLimit),
      rowBuilder: (context, channel, reload) =>
          _ChannelRow(channel: channel, onChanged: reload),
    );
  }
}

class _ChannelRow extends ConsumerStatefulWidget {
  const _ChannelRow({required this.channel, required this.onChanged});

  final AdminChannel channel;
  final VoidCallback onChanged;

  @override
  ConsumerState<_ChannelRow> createState() => _ChannelRowState();
}

class _ChannelRowState extends ConsumerState<_ChannelRow> {
  bool _busy = false;

  AdminChannel get _channel => widget.channel;

  Future<void> _run(
    Future<void> Function() action, {
    required String successKey,
    required String failKey,
  }) async {
    setState(() => _busy = true);
    final l10n = context.l10n;
    try {
      await action();
      if (!mounted) return;
      notifySuccess(context, l10n.t(successKey));
      widget.onChanged();
    } on Object catch (e) {
      if (!mounted) return;
      notifyError(context, l10n.t(failKey), e);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _toggleStatus() => _run(
    () => ref
        .read(adminRepositoryProvider)
        .setChannelStatus(
          _channel.id,
          _channel.isDisabled ? ChannelStatus.active : ChannelStatus.disabled,
        ),
    successKey: 'admin.channelUpdated',
    failKey: 'admin.updateFailed',
  );

  Future<void> _delete() async {
    final l10n = context.l10n;
    final ok = await showAdaptiveConfirm(
      context,
      title: l10n.t('admin.deleteChannel'),
      message: l10n
          .t('admin.deleteChannelConfirm')
          .replaceAll('{name}', _channel.name),
      confirmLabel: l10n.t('common.delete'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!ok || !mounted) return;
    await _run(
      () => ref.read(adminRepositoryProvider).deleteChannel(_channel.id),
      successKey: 'admin.channelDeleted',
      failKey: 'admin.deleteFailed',
    );
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final theme = Theme.of(context);

    return ListTile(
      title: Text(_channel.name, maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            if (_channel.isDisabled)
              AdminBadge(
                label: l10n.t('admin.statusDisabled'),
                color: theme.colorScheme.error,
              ),
            Text(
              _channel.ownerEmail.isEmpty ? '—' : _channel.ownerEmail,
              style: theme.textTheme.bodySmall,
            ),
            Text(
              _channel.channel.joinRef,
              style: theme.textTheme.bodySmall?.copyWith(
                fontFeatures: const [FontFeature.tabularFigures()],
              ),
            ),
          ],
        ),
      ),
      onTap: () => context.push('/admin/channels/${_channel.id}'),
      trailing: _busy
          ? const SizedBox(
              width: 24,
              height: 24,
              child: Center(child: AdaptiveProgress(size: 18)),
            )
          : Builder(
              builder: (anchor) => IconButton(
                icon: const Icon(Icons.more_horiz),
                tooltip: l10n.t('admin.actions'),
                onPressed: () => showGlassMenu(anchor, [
                  GlassMenuAction(
                    label: l10n.t('admin.viewMessages'),
                    icon: Icons.article_outlined,
                    onSelected: () =>
                        context.push('/admin/channels/${_channel.id}'),
                  ),
                  GlassMenuAction(
                    label: _channel.isDisabled
                        ? l10n.t('admin.enable')
                        : l10n.t('admin.disable'),
                    icon: _channel.isDisabled
                        ? Icons.toggle_on_outlined
                        : Icons.toggle_off_outlined,
                    onSelected: _toggleStatus,
                  ),
                  GlassMenuAction(
                    label: l10n.t('common.delete'),
                    icon: Icons.delete_outline,
                    destructive: true,
                    onSelected: _delete,
                  ),
                ]),
              ),
            ),
    );
  }
}
