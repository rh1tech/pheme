import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive.dart';

/// Extracts a join reference from scanned (or pasted) text. When the text is a
/// URL carrying a `ref` query parameter (e.g. `https://pheme.app/join?ref=foo`)
/// that value is used; otherwise the raw text is returned trimmed.
String extractRef(String raw) {
  final trimmed = raw.trim();
  final uri = Uri.tryParse(trimmed);
  final ref = uri?.queryParameters['ref'];
  if (ref != null && ref.trim().isNotEmpty) return ref.trim();
  return trimmed;
}

/// Full-screen camera scanner. Pops with the decoded [String] on the first
/// barcode, or null when dismissed.
///
/// By default the value is run through [extractRef], which is what joining a
/// channel wants. Set [raw] to take the scanned text exactly as it was encoded
/// — a self-hosted server URL is scanned that way, since a `ref` parameter in
/// one would be an ordinary part of the address rather than a join reference.
class QrScannerPage extends StatefulWidget {
  const QrScannerPage({super.key, this.raw = false, this.instruction});

  final bool raw;

  /// Overrides the on-screen prompt, which otherwise talks about channels.
  final String? instruction;

  @override
  State<QrScannerPage> createState() => _QrScannerPageState();
}

class _QrScannerPageState extends State<QrScannerPage> {
  bool _handled = false;

  void _onDetect(BarcodeCapture capture) {
    if (_handled) return;
    for (final barcode in capture.barcodes) {
      final value = barcode.rawValue;
      if (value != null && value.isNotEmpty) {
        _handled = true;
        Navigator.of(
          context,
        ).pop(widget.raw ? value.trim() : extractRef(value));
        return;
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = context.l10n;
    final ios = isCupertino(context);
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        children: [
          Positioned.fill(child: MobileScanner(onDetect: _onDetect)),
          // Reticle to guide aim.
          Center(
            child: Container(
              width: 240,
              height: 240,
              decoration: BoxDecoration(
                border: Border.all(color: Colors.white70, width: 2),
                borderRadius: BorderRadius.circular(20),
              ),
            ),
          ),
          SafeArea(
            child: Column(
              children: [
                Align(
                  alignment: Alignment.centerLeft,
                  child: IconButton(
                    icon: Icon(
                      ios ? CupertinoIcons.xmark : Icons.close,
                      color: Colors.white,
                    ),
                    tooltip: l10n.t('common.cancel'),
                    onPressed: () => Navigator.of(context).pop(),
                  ),
                ),
                const Spacer(),
                Padding(
                  padding: const EdgeInsets.fromLTRB(24, 0, 24, 32),
                  child: Text(
                    widget.instruction ?? l10n.t('scan.instruction'),
                    textAlign: TextAlign.center,
                    style: const TextStyle(
                      color: Colors.white,
                      fontSize: 15,
                      decoration: TextDecoration.none,
                    ),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
