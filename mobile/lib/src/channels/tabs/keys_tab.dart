import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/format.dart';
import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../l10n/app_localizations.dart';
import '../../models/models.dart';
import '../../widgets/error_view.dart';

/// Manage a channel's API keys: list active keys, create new ones (the secret
/// is shown once), and revoke.
class KeysTab extends ConsumerStatefulWidget {
  const KeysTab({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<KeysTab> createState() => _KeysTabState();
}

class _KeysTabState extends ConsumerState<KeysTab> {
  List<ApiKey> _keys = const [];
  bool _loading = true;
  bool _error = false;
  bool _creating = false;

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
      final keys = await ref
          .read(repositoryProvider)
          .listKeys(widget.channelId);
      if (!mounted) return;
      setState(() {
        _keys = keys.where((k) => !k.revoked).toList();
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

  Future<void> _create() async {
    setState(() => _creating = true);
    final l10n = context.l10n;
    try {
      final created = await ref
          .read(repositoryProvider)
          .createKey(widget.channelId);
      await _load();
      if (mounted) await _showCreatedKey(created);
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.keyFailed'), e);
    } finally {
      if (mounted) setState(() => _creating = false);
    }
  }

  Future<void> _showCreatedKey(CreatedKey created) {
    final l10n = context.l10n;
    return showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(l10n.t('channel.keyCreatedTitle')),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              l10n.t('channel.keyShownOnce'),
              style: TextStyle(
                color: Theme.of(dialogContext).colorScheme.onSurfaceVariant,
                fontSize: 13,
              ),
            ),
            const SizedBox(height: 12),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Theme.of(
                  dialogContext,
                ).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(10),
              ),
              child: SelectableText(
                created.key,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: Text(l10n.t('common.close')),
          ),
          FilledButton.icon(
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: created.key));
              if (dialogContext.mounted) Navigator.of(dialogContext).pop();
              if (mounted) notifySuccess(context, l10n.t('channel.keyCopied'));
            },
            icon: const Icon(Icons.copy, size: 16),
            label: Text(l10n.t('channel.copyKey')),
          ),
        ],
      ),
    );
  }

  Future<void> _revoke(ApiKey key) async {
    final l10n = context.l10n;
    try {
      await ref.read(repositoryProvider).revokeKey(widget.channelId, key.id);
      await _load();
      if (mounted) notifySuccess(context, l10n.t('channel.keyRevoked'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.revokeFailed'), e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error) {
      return ErrorView(message: l10n.t('channels.loadFailed'), onRetry: _load);
    }
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Align(
            alignment: Alignment.centerRight,
            child: FilledButton.tonalIcon(
              onPressed: _creating ? null : _create,
              icon: _creating
                  ? const SizedBox(
                      height: 16,
                      width: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.add, size: 18),
              label: Text(l10n.t('channel.createKey')),
            ),
          ),
        ),
        Expanded(
          child: _keys.isEmpty
              ? Center(
                  child: Text(
                    l10n.t('channel.noKeys'),
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                  ),
                )
              : ListView.separated(
                  padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
                  itemCount: _keys.length,
                  separatorBuilder: (_, _) => const SizedBox(height: 8),
                  itemBuilder: (context, i) {
                    final k = _keys[i];
                    return Card(
                      child: ListTile(
                        title: Text(
                          '${k.prefix}…',
                          style: const TextStyle(
                            fontFamily: 'monospace',
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                        subtitle: Text(
                          '${l10n.t('channel.colCreated')}: ${formatDate(k.createdAt)}',
                          style: const TextStyle(fontSize: 12),
                        ),
                        trailing: IconButton(
                          tooltip: l10n.t('channel.revoke'),
                          icon: Icon(
                            Icons.delete_outline,
                            color: Theme.of(context).colorScheme.error,
                          ),
                          onPressed: () => _revoke(k),
                        ),
                      ),
                    );
                  },
                ),
        ),
      ],
    );
  }
}
