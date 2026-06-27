import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../data/app_providers.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/adaptive/adaptive.dart';

/// Channel settings: rename and change subscription mode, subscribe this device
/// to push, and delete the channel.
class ChannelSettingsTab extends ConsumerStatefulWidget {
  const ChannelSettingsTab({super.key, required this.channel});

  final Channel channel;

  @override
  ConsumerState<ChannelSettingsTab> createState() => _ChannelSettingsTabState();
}

class _ChannelSettingsTabState extends ConsumerState<ChannelSettingsTab> {
  late final TextEditingController _name = TextEditingController(
    text: widget.channel.name,
  );
  late SubscriptionMode _mode = widget.channel.subscriptionMode;
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
    super.dispose();
  }

  Future<void> _loadSubscription() async {
    final deviceId = ref.read(deviceControllerProvider);
    if (deviceId == null) return;
    try {
      final status = await ref
          .read(repositoryProvider)
          .channelSubscription(widget.channel.id, deviceId);
      if (mounted) setState(() => _subStatus = status);
    } catch (_) {
      // status stays "none" — subscribing self-heals it
    }
  }

  Future<void> _save() async {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    setState(() => _saving = true);
    final l10n = context.l10n;
    try {
      await ref
          .read(repositoryProvider)
          .updateChannel(widget.channel.id, name: name, mode: _mode);
      await ref.read(channelsProvider.notifier).refresh();
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
      await ref.read(repositoryProvider).subscribe(widget.channel.id, deviceId);
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
      await ref
          .read(repositoryProvider)
          .unsubscribe(widget.channel.id, deviceId);
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
      message: l10n.tp('channel.deleteConfirm', {'name': widget.channel.name}),
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
      await ref.read(repositoryProvider).deleteChannel(widget.channel.id);
      await ref.read(channelsProvider.notifier).refresh();
      if (mounted) {
        notifySuccess(context, l10n.t('channel.channelDeleted'));
        context.go('/');
      }
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.deleteFailed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;
    final registered = ref.watch(deviceControllerProvider) != null;

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        AdaptiveTextField(
          controller: _name,
          label: l10n.t('channels.channelName'),
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

        // Subscription card
        AdaptiveCard(
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
                        onPressed: (_subBusy || !registered)
                            ? null
                            : _subscribe,
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
        ),
        const SizedBox(height: 16),

        // Danger zone
        Card(
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(16),
            side: BorderSide(color: scheme.error.withValues(alpha: 0.5)),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  l10n.t('channel.dangerTitle'),
                  style: const TextStyle(
                    fontWeight: FontWeight.w600,
                    fontSize: 16,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  l10n.t('channel.dangerDescription'),
                  style: TextStyle(
                    color: scheme.onSurfaceVariant,
                    fontSize: 13,
                  ),
                ),
                const SizedBox(height: 12),
                Align(
                  alignment: Alignment.centerRight,
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
                ),
              ],
            ),
          ),
        ),
      ],
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
