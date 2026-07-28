// Which server this app talks to: chosen at sign-in, shown afterwards.
//
// It used to be editable ONLY from Settings, which is behind the sign-in screen — so somebody
// running their own Pheme could not point the app at it without first signing in to a server they
// have no account on. It is a field on the sign-in form now.
//
// And it is editable ONLY there. Everything an account has on a device — the session, the push
// registration, the MLS identity, the keys — belongs to the server it was made on, and none of it
// survives being repointed. Settings shows the address and can hand it on as a QR; changing it is
// signing out.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../channels/qr_scanner_page.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive_text_field.dart';
import '../widgets/glass/glass.dart';
import 'providers.dart';
import 'validators.dart';
import 'snackbar.dart';

/// Shows the current server as a QR code, for handing to somebody standing next to you.
///
/// The mirror of the scanner. An operator's address is a hostname plus twelve meaningless
/// case-sensitive hex characters, which is unreadable over the phone and mistyped on sight — so the
/// person who already has it working should be able to show it rather than dictate it.
///
/// White plate under the code whatever the theme is: a QR is read by a camera, not by a person, and
/// inverted codes are a coin toss across scanners.
Future<void> showServerQr(BuildContext context, WidgetRef ref) async {
  final l10n = AppLocalizations.of(context);
  final url = ref.read(settingsControllerProvider).baseUrl;
  if (url.isEmpty) {
    notifyError(context, l10n.t('settings.serverInvalid'));
    return;
  }
  final scheme = Theme.of(context).colorScheme;

  await showGlassDialog<void>(
    context: context,
    builder: (dialogContext) => GlassDialog(
      title: Text(l10n.t('settings.serverShare')),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(l10n.t('settings.serverShareHint')),
          const SizedBox(height: 16),
          Center(
            child: Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Colors.white,
                borderRadius: BorderRadius.circular(12),
              ),
              child: QrImageView(
                data: url,
                size: 190,
                backgroundColor: Colors.white,
              ),
            ),
          ),
          const SizedBox(height: 16),
          // The address in full, and selectable. A QR is no use over a phone call, and the prefix is
          // exactly the part somebody reading it aloud will get wrong.
          SelectableText(
            url,
            textAlign: TextAlign.center,
            style: TextStyle(
              fontSize: 12,
              fontFamily: 'monospace',
              color: scheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
      actions: [
        GlassDialogAction(
          label: l10n.t('common.copy'),
          onPressed: () async {
            await Clipboard.setData(ClipboardData(text: url));
            if (dialogContext.mounted) Navigator.of(dialogContext).pop();
            if (context.mounted) {
              notifySuccess(context, l10n.t('common.copied'));
            }
          },
        ),
        GlassDialogAction(
          label: l10n.t('common.close'),
          emphasised: true,
          onPressed: () => Navigator.of(dialogContext).pop(),
        ),
      ],
    ),
  );
}

/// The server field, as it appears on every screen you can reach without an account.
///
/// A FIELD, not a setting tucked away behind the form. Pheme is self-hosted as often as not, and for
/// those users the address is not a preference — it is part of signing in, as much as the email is.
/// Burying it in Settings put it behind the very screen that needs it; showing it as a quiet line
/// under the card still implied the app already knew where to go.
///
/// The QR button matters more than it looks. A self-hosted server lives at an unlisted path prefix —
/// `https://host.example/a7f3c91e4b2d` — long, case-sensitive and meaningless, which is exactly the
/// kind of string people mistype once and give up on. The self-host kit prints the QR for the
/// operator to hand out.
class ServerFormField extends ConsumerStatefulWidget {
  const ServerFormField({
    super.key,
    required this.controller,
    this.enabled = true,
  });

  final TextEditingController controller;
  final bool enabled;

  @override
  ConsumerState<ServerFormField> createState() => _ServerFormFieldState();
}

class _ServerFormFieldState extends ConsumerState<ServerFormField> {
  Future<void> _scan() async {
    final l10n = AppLocalizations.of(context);
    final scanned = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        fullscreenDialog: true,
        builder: (_) => QrScannerPage(
          raw: true,
          instruction: l10n.t('settings.serverScanHint'),
        ),
      ),
    );
    if (scanned == null || scanned.isEmpty || !mounted) return;
    // Straight into the field rather than saved behind the user's back: it is theirs to look at and
    // correct before they sign in with it, and a QR that scanned the wrong thing should be visible
    // rather than merely take effect.
    setState(() => widget.controller.text = scanned.trim());
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);

    return Row(
      // Centred against the field, rather than pinned to the top of the row.
      //
      // The button was offset by a fixed padding chosen for a field with a label ABOVE it — which is
      // what Material draws and iOS does not, since there the label is the placeholder INSIDE the
      // box. So on iOS the button sat below the field's centre by exactly the height of a label that
      // was never drawn. Centring asks the row rather than guessing at either platform's decoration.
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Expanded(
          child: AdaptiveTextFormField(
            controller: widget.controller,
            label: l10n.t('settings.serverUrl'),
            keyboardType: TextInputType.url,
            validator: (value) => isValidServerUrl(value ?? '')
                ? null
                : l10n.t('settings.serverInvalid'),
          ),
        ),
        const SizedBox(width: 8),
        GlassIconButton(
          icon: Icons.qr_code_scanner_rounded,
          semanticLabel: l10n.t('settings.serverScan'),
          onPressed: widget.enabled ? _scan : null,
        ),
      ],
    );
  }
}
