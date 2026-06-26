import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/providers.dart';
import '../../core/snackbar.dart';
import '../../l10n/app_localizations.dart';

/// Lets a channel owner send a notification from the app (uses their JWT via
/// POST /v1/channels/{id}/notify — the one bridge into the ingest path).
class SendTab extends ConsumerStatefulWidget {
  const SendTab({super.key, required this.channelId});

  final String channelId;

  @override
  ConsumerState<SendTab> createState() => _SendTabState();
}

class _SendTabState extends ConsumerState<SendTab> {
  final _title = TextEditingController();
  final _body = TextEditingController();
  bool _sending = false;

  @override
  void dispose() {
    _title.dispose();
    _body.dispose();
    super.dispose();
  }

  bool get _canSend =>
      _title.text.trim().isNotEmpty || _body.text.trim().isNotEmpty;

  Future<void> _send() async {
    if (!_canSend) return;
    FocusScope.of(context).unfocus();
    setState(() => _sending = true);
    final l10n = context.l10n;
    try {
      await ref
          .read(repositoryProvider)
          .notifyChannel(
            widget.channelId,
            _title.text.trim(),
            _body.text.trim(),
          );
      if (!mounted) return;
      _title.clear();
      _body.clear();
      notifySuccess(context, l10n.t('channel.messageSent'));
    } catch (e) {
      if (mounted) notifyError(context, l10n.t('channel.sendFailed'), e);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        TextField(
          controller: _title,
          textInputAction: TextInputAction.next,
          onChanged: (_) => setState(() {}),
          decoration: InputDecoration(
            labelText: l10n.t('channel.sendTitle'),
            hintText: l10n.t('channel.titlePlaceholder'),
          ),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _body,
          minLines: 3,
          maxLines: 6,
          onChanged: (_) => setState(() {}),
          decoration: InputDecoration(
            labelText: l10n.t('channel.sendBody'),
            hintText: l10n.t('channel.bodyPlaceholder'),
            alignLabelWithHint: true,
          ),
        ),
        const SizedBox(height: 16),
        FilledButton.icon(
          onPressed: (_sending || !_canSend) ? null : _send,
          icon: _sending
              ? const SizedBox(
                  height: 18,
                  width: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.send_rounded, size: 18),
          label: Text(l10n.t('channel.send')),
        ),
      ],
    );
  }
}
