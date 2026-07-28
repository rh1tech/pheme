// A channel's own settings, as a screen.
//
// These lived in a sheet alongside everything else you can do to a channel — sharing it, its
// notifications, its subscribers, its keys, deleting it — so one surface held five unrelated jobs
// and the two text fields at the top were the least of them. Each of those is its own entry in the
// menu now, and this is the one that changes what the channel IS.
//
// Laid out like the app's own settings and like the profile form: captioned fields, a row that
// opens a chooser for the thing with a fixed set of answers, and one explicit Save. Nothing here
// applies as you type — a channel's name and its phetag are public, and a half-typed phetag is not
// something to publish on every keystroke.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/validators.dart';
import '../core/snackbar.dart';
import '../data/app_providers.dart';
import '../l10n/app_localizations.dart';
import '../models/models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';
import '../widgets/glass/glass.dart';

class ChannelSettingsPage extends ConsumerWidget {
  const ChannelSettingsPage({super.key, required this.channelId});

  final String channelId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = context.l10n;
    final relation = ref.watch(channelRelationProvider(channelId));

    return relation.when(
      loading: () =>
          const AdaptiveScaffold(body: Center(child: AdaptiveProgress())),
      error: (e, _) => AdaptiveScaffold(
        body: ErrorView(
          message: l10n.t('channel.notFound'),
          onRetry: () => ref.invalidate(channelRelationProvider(channelId)),
        ),
      ),
      data: (rel) => _ChannelSettingsForm(relation: rel),
    );
  }
}

class _ChannelSettingsForm extends ConsumerStatefulWidget {
  const _ChannelSettingsForm({required this.relation});

  final ChannelRelation relation;

  @override
  ConsumerState<_ChannelSettingsForm> createState() =>
      _ChannelSettingsFormState();
}

class _ChannelSettingsFormState extends ConsumerState<_ChannelSettingsForm> {
  Channel get _channel => widget.relation.channel;

  late final TextEditingController _name = TextEditingController(
    text: _channel.name,
  );
  late final TextEditingController _alias = TextEditingController(
    text: _channel.alias ?? '',
  );
  late SubscriptionMode _mode = _channel.subscriptionMode;
  bool _saving = false;

  @override
  void dispose() {
    _name.dispose();
    _alias.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    final l10n = context.l10n;

    // Only sent when it CHANGED. A phetag is claimed globally and the server refuses a duplicate, so
    // re-sending the one this channel already holds would have it refuse the channel its own name.
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

  Future<void> _pickMode(AppLocalizations l10n) async {
    final picked = await showGlassDialog<SubscriptionMode>(
      context: context,
      builder: (dialogContext) => GlassDialog(
        title: Text(l10n.t('channels.subscriptionMode')),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            for (final option in SubscriptionMode.values)
              _ModeRow(
                label: l10n.t(
                  option == SubscriptionMode.open
                      ? 'mode.open'
                      : 'mode.approval',
                ),
                detail: l10n.t(
                  option == SubscriptionMode.open
                      ? 'mode.openHint'
                      : 'mode.approvalHint',
                ),
                selected: option == _mode,
                onTap: () => Navigator.of(dialogContext).pop(option),
              ),
          ],
        ),
      ),
    );
    if (picked != null && mounted) setState(() => _mode = picked);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final scheme = Theme.of(context).colorScheme;

    final aliasText = _alias.text.trim();
    final aliasInvalid = aliasText.isNotEmpty && !isPhetagValid(aliasText);

    return AdaptiveScaffold(
      title: Text(l10n.t('channel.tabSettings')),
      body: ListView(
        padding: const EdgeInsets.fromLTRB(20, 20, 20, 40),
        children: [
          AdaptiveTextField(
            controller: _name,
            label: l10n.t('channels.channelName'),
          ),
          const SizedBox(height: 20),
          AdaptiveTextField(
            controller: _alias,
            label: l10n.t('channel.phetag'),
            placeholder: l10n.t('channel.phetagPlaceholder'),
            maxLength: 24,
            onChanged: (_) => setState(() {}),
          ),
          const SizedBox(height: 6),
          Padding(
            padding: const EdgeInsets.only(left: 4),
            child: Text(
              aliasInvalid
                  ? l10n.t('channel.phetagInvalid')
                  : l10n.t('channel.phetagHint'),
              style: TextStyle(
                fontSize: 12,
                color: aliasInvalid ? scheme.error : scheme.onSurfaceVariant,
              ),
            ),
          ),
          const SizedBox(height: 24),
          // A row that opens a chooser, exactly as the app's own settings do for theme and
          // language — rather than a segmented control, which is a third way of asking the same
          // shape of question on a screen that already has two.
          _SettingRow(
            icon: Icons.how_to_reg_outlined,
            title: l10n.t('channels.subscriptionMode'),
            value: l10n.t(
              _mode == SubscriptionMode.open ? 'mode.open' : 'mode.approval',
            ),
            onTap: () => _pickMode(l10n),
          ),
          const SizedBox(height: 28),
          AdaptiveButton.filled(
            onPressed: _saving ? null : _save,
            child: _saving
                ? const AdaptiveProgress(size: 18)
                : Text(l10n.t('channel.saveChanges')),
          ),
        ],
      ),
    );
  }
}

/// A row that states a setting's current answer and opens a chooser for it.
class _SettingRow extends StatelessWidget {
  const _SettingRow({
    required this.icon,
    required this.title,
    required this.value,
    required this.onTap,
  });

  final IconData icon;
  final String title;
  final String value;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Material(
      color: scheme.onSurface.withValues(alpha: 0.04),
      borderRadius: BorderRadius.circular(GlassMetrics.fieldRadius),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(GlassMetrics.fieldRadius),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
          child: Row(
            children: [
              Icon(icon, size: 20, color: scheme.onSurfaceVariant),
              const SizedBox(width: 12),
              Expanded(
                child: Text(title, style: const TextStyle(fontSize: 15)),
              ),
              Text(
                value,
                style: TextStyle(fontSize: 15, color: scheme.onSurfaceVariant),
              ),
              const SizedBox(width: 4),
              Icon(
                Icons.chevron_right,
                size: 18,
                color: scheme.onSurfaceVariant,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// One option in the subscription-mode chooser.
class _ModeRow extends StatelessWidget {
  const _ModeRow({
    required this.label,
    required this.detail,
    required this.selected,
    required this.onTap,
  });

  final String label;
  final String detail;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 11),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    label,
                    style: TextStyle(
                      fontSize: 16,
                      fontWeight: selected ? FontWeight.w600 : FontWeight.w400,
                      color: scheme.onSurface,
                    ),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    detail,
                    style: TextStyle(
                      fontSize: 13,
                      color: scheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
            if (selected) ...[
              const SizedBox(width: 12),
              Icon(Icons.check, size: 20, color: scheme.primary),
            ],
          ],
        ),
      ),
    );
  }
}
