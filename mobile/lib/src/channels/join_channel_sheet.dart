import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';
import 'qr_scanner_page.dart';

/// Bottom sheet that collects a join reference — a trigger ID or phetag typed in,
/// or scanned from a QR code. Returns the reference [String] on submit, or null
/// on dismiss.
class JoinChannelSheet extends StatefulWidget {
  const JoinChannelSheet({super.key});

  @override
  State<JoinChannelSheet> createState() => _JoinChannelSheetState();
}

class _JoinChannelSheetState extends State<JoinChannelSheet> {
  final _ref = TextEditingController();

  @override
  void dispose() {
    _ref.dispose();
    super.dispose();
  }

  void _submit() {
    final ref = extractRef(_ref.text);
    if (ref.isEmpty) return;
    Navigator.of(context).pop(ref);
  }

  Future<void> _scan() async {
    final scanned = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        fullscreenDialog: true,
        builder: (_) => const QrScannerPage(),
      ),
    );
    if (scanned == null || scanned.isEmpty || !mounted) return;
    // A scan resolves directly to a join.
    Navigator.of(context).pop(scanned);
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final ios = isCupertino(context);
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
                l10n.t('join.title'),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 16),
              AdaptiveTextField(
                controller: _ref,
                autofocus: true,
                label: l10n.t('join.refLabel'),
                placeholder: l10n.t('join.refHint'),
                textInputAction: TextInputAction.go,
                onSubmitted: (_) => _submit(),
              ),
              const SizedBox(height: 12),
              AdaptiveButton.outlined(
                onPressed: _scan,
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(
                      ios
                          ? CupertinoIcons.qrcode_viewfinder
                          : Icons.qr_code_scanner,
                      size: 18,
                    ),
                    const SizedBox(width: 8),
                    Text(l10n.t('join.scanQr')),
                  ],
                ),
              ),
              const SizedBox(height: 20),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  AdaptiveButton.text(
                    onPressed: () => Navigator.of(context).pop(),
                    child: Text(l10n.t('common.cancel')),
                  ),
                  const SizedBox(width: 8),
                  AdaptiveButton.filled(
                    onPressed: _submit,
                    child: Text(l10n.t('join.action')),
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
