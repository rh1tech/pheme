import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import '../models/models.dart';

/// Bottom sheet that collects a new channel's name and subscription mode.
/// Returns `(name, mode)` on submit, or null on dismiss.
class CreateChannelSheet extends StatefulWidget {
  const CreateChannelSheet({super.key});

  @override
  State<CreateChannelSheet> createState() => _CreateChannelSheetState();
}

class _CreateChannelSheetState extends State<CreateChannelSheet> {
  final _name = TextEditingController();
  SubscriptionMode _mode = SubscriptionMode.approval;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  void _submit() {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    Navigator.of(context).pop((name: name, mode: _mode));
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final insets = MediaQuery.viewInsetsOf(context);
    return Padding(
      padding: EdgeInsets.only(bottom: insets.bottom),
      child: SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Center(
                child: Container(
                  width: 40,
                  height: 4,
                  margin: const EdgeInsets.only(bottom: 16),
                  decoration: BoxDecoration(
                    color: Theme.of(context).colorScheme.outlineVariant,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              Text(
                l10n.t('channels.newChannel'),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 16),
              TextField(
                controller: _name,
                autofocus: true,
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submit(),
                decoration: InputDecoration(
                  labelText: l10n.t('channels.channelName'),
                  hintText: l10n.t('channels.channelNamePlaceholder'),
                ),
              ),
              const SizedBox(height: 16),
              Text(
                l10n.t('channels.subscriptionMode'),
                style: const TextStyle(fontWeight: FontWeight.w500),
              ),
              const SizedBox(height: 8),
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
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  TextButton(
                    onPressed: () => Navigator.of(context).pop(),
                    child: Text(l10n.t('common.cancel')),
                  ),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: _submit,
                    child: Text(l10n.t('channels.create')),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
