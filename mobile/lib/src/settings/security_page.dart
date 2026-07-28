// "Devices & security": the user's own devices and their end-to-end-encryption status.
//
// Read-mostly. It surfaces what the other phases already produce — whether chats are backed up,
// that history syncs to new devices — and lets the user remove a device they no longer trust, which
// signs it out and cuts it out of every encrypted conversation (MlsService.terminateOwnDevice).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/chat_providers.dart';
import '../core/format.dart';
import '../core/providers.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../models/chat_models.dart';
import '../widgets/adaptive/adaptive.dart';
import '../widgets/error_view.dart';

class SecurityPage extends ConsumerStatefulWidget {
  const SecurityPage({super.key});

  @override
  ConsumerState<SecurityPage> createState() => _SecurityPageState();
}

class _SecurityPageState extends ConsumerState<SecurityPage> {
  List<MLSDevice>? _devices;
  bool _backedUp = false;
  String? _thisDeviceId;
  bool _loading = true;
  bool _error = false;
  String? _removing;

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
    final mls = ref.read(mlsServiceProvider);
    final repo = ref.read(repositoryProvider);
    try {
      final devices = await repo.myDevices();
      final backedUp = await mls.backupExists();
      final thisDeviceId = await mls.currentDeviceId();
      if (!mounted) return;
      setState(() {
        _devices = devices;
        _backedUp = backedUp;
        _thisDeviceId = thisDeviceId;
        _loading = false;
      });
    } on Object {
      if (!mounted) return;
      setState(() {
        _error = true;
        _loading = false;
      });
    }
  }

  Future<void> _remove(MLSDevice device) async {
    final l10n = context.l10n;
    final confirmed = await showAdaptiveConfirm(
      context,
      title: l10n.t('security.remove'),
      message: l10n.t('security.removeConfirm'),
      confirmLabel: l10n.t('security.remove'),
      cancelLabel: l10n.t('common.cancel'),
      isDestructive: true,
    );
    if (!confirmed || !mounted) return;

    setState(() => _removing = device.deviceId);
    final userId = ref.read(myUserIdProvider);
    final mls = ref.read(mlsServiceProvider);
    try {
      await mls.terminateOwnDevice(userId, device.deviceId);
      if (!mounted) return;
      setState(() {
        _devices = _devices
            ?.where((d) => d.deviceId != device.deviceId)
            .toList();
        _removing = null;
      });
      notifySuccess(context, l10n.t('security.removed'));
    } on Object {
      if (!mounted) return;
      setState(() => _removing = null);
      notifyError(context, l10n.t('security.removeFailed'));
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return AdaptiveScaffold(
      title: Text(l10n.t('security.title')),
      body: _body(context, l10n),
    );
  }

  Widget _body(BuildContext context, AppLocalizations l10n) {
    if (_loading) return const Center(child: AdaptiveProgress());
    if (_error) {
      return ErrorView(message: l10n.t('security.loadFailed'), onRetry: _load);
    }
    final devices = _devices ?? const [];
    final theme = Theme.of(context);

    return ListView(
      children: [
        _SectionHeader(title: l10n.t('security.statusHeading')),
        ListTile(
          // Dense, with the padding stated rather than inherited. A stock ListTile is built for a
          // 56pt row with generous air around it, and isThreeLine adds more on top — on a screen
          // that is a list of short facts about devices it left every row swimming, and the page
          // scrolled for a handful of lines.
          dense: true,
          visualDensity: VisualDensity.compact,
          contentPadding: const EdgeInsets.symmetric(horizontal: 16),
          leading: Icon(
            _backedUp ? Icons.verified_user : Icons.gpp_maybe,
            color: _backedUp ? Colors.green : theme.colorScheme.tertiary,
          ),
          title: Text(
            l10n.t(_backedUp ? 'security.backupOn' : 'security.backupOff'),
          ),
          subtitle: Text(
            l10n.t(
              _backedUp ? 'security.backupOnHint' : 'security.backupOffHint',
            ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
          child: Text(
            l10n.t('security.syncOn'),
            style: theme.textTheme.bodySmall,
          ),
        ),
        _SectionHeader(title: l10n.t('security.devicesHeading')),
        if (devices.isEmpty)
          Padding(
            padding: const EdgeInsets.all(16),
            child: Text(l10n.t('security.noDevices')),
          )
        else
          for (final device in devices) _deviceTile(context, l10n, device),
      ],
    );
  }

  Widget _deviceTile(
    BuildContext context,
    AppLocalizations l10n,
    MLSDevice device,
  ) {
    final isThis = device.deviceId == _thisDeviceId;
    final label = device.label.isNotEmpty
        ? device.label
        : device.deviceId.substring(0, device.deviceId.length.clamp(0, 8));
    return ListTile(
      dense: true,
      visualDensity: VisualDensity.compact,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16),
      leading: const Icon(Icons.devices_outlined),
      title: Row(
        children: [
          Flexible(child: Text(label, overflow: TextOverflow.ellipsis)),
          if (isThis) ...[
            const SizedBox(width: 8),
            _ThisDeviceBadge(label: l10n.t('security.thisDevice')),
          ],
        ],
      ),
      subtitle: device.lastSeenAt.isEmpty
          ? null
          : Text(
              l10n.tp('security.lastActive', {
                'when': formatDateTime(device.lastSeenAt),
              }),
            ),
      trailing: isThis
          ? null
          : (_removing == device.deviceId
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: AdaptiveProgress(),
                  )
                : TextButton(
                    onPressed: _removing != null ? null : () => _remove(device),
                    child: Text(l10n.t('security.remove')),
                  )),
    );
  }
}

class _ThisDeviceBadge extends StatelessWidget {
  const _ThisDeviceBadge({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: scheme.secondaryContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        label,
        style: Theme.of(
          context,
        ).textTheme.labelSmall?.copyWith(color: scheme.onSecondaryContainer),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title});

  final String title;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 18, 16, 6),
      child: Text(
        title.toUpperCase(),
        style: theme.textTheme.labelMedium?.copyWith(
          color: theme.colorScheme.primary,
          letterSpacing: 0.6,
        ),
      ),
    );
  }
}
