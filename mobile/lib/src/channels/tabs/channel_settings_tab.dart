import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../core/validators.dart';
import '../../data/app_providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/adaptive/adaptive.dart';

/// Channel settings. For the owner: rename, change subscription mode, set a
/// phetag, share a join QR code, subscribe this device, and delete the channel.
/// For a joined (non-owner) member: subscribe this device and leave the channel.
class ChannelSettingsTab extends ConsumerStatefulWidget {
  const ChannelSettingsTab({super.key, required this.relation});

  final ChannelRelation relation;

  @override
  ConsumerState<ChannelSettingsTab> createState() => _ChannelSettingsTabState();
}

class _ChannelSettingsTabState extends ConsumerState<ChannelSettingsTab> {
  Channel get _channel => widget.relation.channel;
  bool get _isOwner => widget.relation.isOwner;

  late final TextEditingController _name = TextEditingController(
    text: _channel.name,
  );
  late final TextEditingController _alias = TextEditingController(
    text: _channel.alias ?? '',
  );
  late SubscriptionMode _mode = _channel.subscriptionMode;
  bool _saving = false;

  SubscriptionStatus _subStatus = SubscriptionStatus.none;
  bool _subBusy = false;

  @override
  void initState() {
    super.initState();
    _loadSubscription();
  }

  @override
  void dispose() {
    _name.dispose();
    _alias.dispose();
    super.dispose();
  }

  Future<void> _loadSubscription() async {
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) return;
    try {
      final status = await ref
          .read(repositoryProvider)
          .channelSubscription(_channel.id, deviceId);
      if (mounted) setState(() => _subStatus = status);
    } catch (_) {
      // status stays "none" — subscribing self-heals it
    }
  }

  Future<void> _save() async {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    final l10n = context.l10n;
    final aliasText = _alias.text.trim();
    final originalAlias = _channel.alias ?? '';
    String? aliasParam;
    if (aliasText != originalAlias && aliasText.isNotEmpty) {
      if (!isPhetagValid(aliasText)) {
        notifyError(context, l10n.t('channel.phetagInvalid'));
        return;
      }
      aliasParam = aliasText;
    }
    setState(() => _saving = true);
    try {
      await ref
          .read(repositoryProvider)
          .updateChannel(
            _channel.id,
            name: name,
            mode: _mode,
            alias: aliasParam,
          );
      await ref.read(channelsProvider.notifier).refresh();
      ref.invalidate(channelRelationProvider(_channel.id));
      if (mounted) notifySuccess(context, l10n.t('channel.channelUpdated'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.updateFailed'), e);
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _subscribe() async {
    final l10n = context.l10n;
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) {
      notifyError(context, l10n.t('channel.subscribeNeedsDevice'));
      return;
    }
    setState(() => _subBusy = true);
    try {
      await ref.read(repositoryProvider).subscribe(_channel.id, deviceId);
      await _loadSubscription();
      if (mounted) notifySuccess(context, l10n.t('channel.subscribed'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.subscribeFailed'), e);
    } finally {
      if (mounted) setState(() => _subBusy = false);
    }
  }

  Future<void> _unsubscribe() async {
    final l10n = context.l10n;
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) return;
    setState(() => _subBusy = true);
    try {
      await ref.read(repositoryProvider).unsubscribe(_channel.id, deviceId);
      if (mounted) {
        setState(() => _subStatus = SubscriptionStatus.none);
        notifySuccess(context, l10n.t('channel.unsubscribed'));
      }
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.unsubscribeFailed'), e);
    } finally {
      if (mounted) setState(() => _subBusy = false);
    }
  }

  Future<void> _confirmDelete() async {
    final l10n = context.l10n;
    final confirmed = await showAdaptiveConfirm(
      context,
      title: l10n.t('channel.dangerTitle'),
      message: l10n.tp('channel.deleteConfirm', {'name': _channel.name}),
      confirmLabel: l10n.t('common.delete'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!confirmed || !mounted) return;
    await _delete();
  }

  Future<void> _delete() async {
    final l10n = context.l10n;
    try {
      await ref.read(repositoryProvider).deleteChannel(_channel.id);
      await ref.read(channelsProvider.notifier).refresh();
      if (mounted) {
        notifySuccess(context, l10n.t('channel.channelDeleted'));
        context.go('/');
      }
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.deleteFailed'), e);
    }
  }

  Future<void> _confirmLeave() async {
    final l10n = context.l10n;
    final confirmed = await showAdaptiveConfirm(
      context,
      title: l10n.t('channel.leaveTitle'),
      message: l10n.tp('channel.leaveConfirm', {'name': _channel.name}),
      confirmLabel: l10n.t('channel.leaveAction'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!confirmed || !mounted) return;
    await _leave();
  }

  Future<void> _leave() async {
    final l10n = context.l10n;
    try {
      await ref.read(repositoryProvider).leaveChannel(_channel.id);
      await ref.read(joinedChannelsProvider.notifier).refresh();
      if (mounted) {
        notifySuccess(context, l10n.t('channel.left'));
        context.go('/');
      }
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.leaveFailed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (_isOwner) ..._ownerControls(l10n),
        _subscriptionCard(l10n),
        const SizedBox(height: 16),
        if (_isOwner) _dangerZone(l10n) else _leaveZone(l10n),
      ],
    );
  }

  List<Widget> _ownerControls(AppLocalizations l10n) {
    final aliasText = _alias.text.trim();
    final aliasInvalid = aliasText.isNotEmpty && !isPhetagValid(aliasText);
    final scheme = Theme.of(context).colorScheme;
    return [
      AdaptiveTextField(
        controller: _name,
        label: l10n.t('channels.channelName'),
      ),
      const SizedBox(height: 16),
      Text(
        l10n.t('channel.phetag'),
        style: const TextStyle(fontWeight: FontWeight.w500),
      ),
      const SizedBox(height: 8),
      AdaptiveTextField(
        controller: _alias,
        placeholder: l10n.t('channel.phetag'),
        maxLength: 24,
        onChanged: (_) => setState(() {}),
      ),
      const SizedBox(height: 4),
      Text(
        aliasInvalid
            ? l10n.t('channel.phetagInvalid')
            : l10n.t('channel.phetagHint'),
        style: TextStyle(
          fontSize: 12,
          color: aliasInvalid ? scheme.error : scheme.onSurfaceVariant,
        ),
      ),
      const SizedBox(height: 16),
      Text(
        l10n.t('channels.subscriptionMode'),
        style: const TextStyle(fontWeight: FontWeight.w500),
      ),
      const SizedBox(height: 8),
      if (isCupertino(context))
        SizedBox(
          width: double.infinity,
          child: CupertinoSlidingSegmentedControl<SubscriptionMode>(
            groupValue: _mode,
            onValueChanged: (m) {
              if (m != null) setState(() => _mode = m);
            },
            children: {
              SubscriptionMode.approval: Text(l10n.t('mode.approval')),
              SubscriptionMode.open: Text(l10n.t('mode.open')),
            },
          ),
        )
      else
        SegmentedButton<SubscriptionMode>(
          segments: [
            ButtonSegment(
              value: SubscriptionMode.approval,
              label: Text(l10n.t('mode.approval')),
            ),
            ButtonSegment(
              value: SubscriptionMode.open,
              label: Text(l10n.t('mode.open')),
            ),
          ],
          selected: {_mode},
          onSelectionChanged: (s) => setState(() => _mode = s.first),
        ),
      const SizedBox(height: 16),
      Align(
        alignment: Alignment.centerRight,
        child: AdaptiveButton.filled(
          onPressed: _saving ? null : _save,
          child: _saving
              ? const AdaptiveProgress(size: 18)
              : Text(l10n.t('channel.saveChanges')),
        ),
      ),
      const SizedBox(height: 24),
      _shareCard(l10n),
      const SizedBox(height: 16),
    ];
  }

  Widget _shareCard(AppLocalizations l10n) {
    final scheme = Theme.of(context).colorScheme;
    final ref_ = _channel.joinRef;
    return AdaptiveCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            l10n.t('channel.shareTitle'),
            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
          ),
          const SizedBox(height: 6),
          Text(
            l10n.t('channel.shareDescription'),
            style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
          ),
          const SizedBox(height: 16),
          Center(
            child: Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Colors.white,
                borderRadius: BorderRadius.circular(12),
              ),
              child: QrImageView(
                data: ref_,
                size: 180,
                backgroundColor: Colors.white,
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text(
            l10n.t('channel.shareRef'),
            style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
          ),
          const SizedBox(height: 4),
          Row(
            children: [
              Expanded(
                child: Text(
                  ref_,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    fontFamily: 'monospace',
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              AdaptiveIconButton(
                icon: isCupertino(context)
                    ? CupertinoIcons.doc_on_doc
                    : Icons.copy,
                semanticLabel: l10n.t('common.copy'),
                onPressed: () async {
                  await Clipboard.setData(ClipboardData(text: ref_));
                  if (mounted) {
                    notifySuccess(context, l10n.t('channel.refCopied'));
                  }
                },
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _subscriptionCard(AppLocalizations l10n) {
    final scheme = Theme.of(context).colorScheme;
    final registered = ref.watch(deviceControllerProvider) != null;
    return AdaptiveCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  l10n.t('channel.subscribeTitle'),
                  style: const TextStyle(
                    fontWeight: FontWeight.w600,
                    fontSize: 16,
                  ),
                ),
              ),
              if (_subStatus == SubscriptionStatus.active)
                _StatusChip(
                  label: l10n.t('channel.subscribed'),
                  color: scheme.tertiary,
                ),
              if (_subStatus == SubscriptionStatus.pending)
                _StatusChip(
                  label: l10n.t('channel.subscriptionPending'),
                  color: scheme.secondary,
                ),
            ],
          ),
          const SizedBox(height: 6),
          Text(
            l10n.t('channel.subscribeDescription'),
            style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
          ),
          if (!registered) ...[
            const SizedBox(height: 8),
            Text(
              l10n.t('channel.subscribeNeedsDevice'),
              style: TextStyle(color: scheme.error, fontSize: 12),
            ),
          ],
          const SizedBox(height: 12),
          Align(
            alignment: Alignment.centerRight,
            child: _subStatus == SubscriptionStatus.none
                ? AdaptiveButton.outlined(
                    onPressed: (_subBusy || !registered) ? null : _subscribe,
                    child: Text(l10n.t('channel.subscribe')),
                  )
                : AdaptiveButton.outlined(
                    isDestructive: true,
                    onPressed: _subBusy ? null : _unsubscribe,
                    child: Text(l10n.t('channel.unsubscribe')),
                  ),
          ),
        ],
      ),
    );
  }

  Widget _dangerZone(AppLocalizations l10n) {
    final scheme = Theme.of(context).colorScheme;
    return _OutlinedZone(
      title: l10n.t('channel.dangerTitle'),
      description: l10n.t('channel.dangerDescription'),
      borderColor: scheme.error.withValues(alpha: 0.5),
      child: AdaptiveButton.outlined(
        isDestructive: true,
        onPressed: _confirmDelete,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.delete_outline, size: 18),
            const SizedBox(width: 8),
            Text(l10n.t('common.delete')),
          ],
        ),
      ),
    );
  }

  Widget _leaveZone(AppLocalizations l10n) {
    final scheme = Theme.of(context).colorScheme;
    return _OutlinedZone(
      title: l10n.t('channel.leaveTitle'),
      description: l10n.t('channel.leaveDescription'),
      borderColor: scheme.error.withValues(alpha: 0.5),
      child: AdaptiveButton.outlined(
        isDestructive: true,
        onPressed: _confirmLeave,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.logout, size: 18),
            const SizedBox(width: 8),
            Text(l10n.t('channel.leaveAction')),
          ],
        ),
      ),
    );
  }
}

class _OutlinedZone extends StatelessWidget {
  const _OutlinedZone({
    required this.title,
    required this.description,
    required this.child,
    required this.borderColor,
  });

  final String title;
  final String description;
  final Widget child;
  final Color borderColor;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Card(
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: borderColor),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              title,
              style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 16),
            ),
            const SizedBox(height: 6),
            Text(
              description,
              style: TextStyle(color: scheme.onSurfaceVariant, fontSize: 13),
            ),
            const SizedBox(height: 12),
            Align(alignment: Alignment.centerRight, child: child),
          ],
        ),
      ),
    );
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
