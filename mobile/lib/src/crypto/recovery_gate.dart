// The recovery flow, as a gate wrapped around the signed-in surface.
//
// It does the two things a device has to be walked through exactly once, mirroring the web client's
// KeyBackup gates:
//
//   * a device the session load refuses to mint an identity for — a FRESH one that finds a backup
//     waiting, or one whose identity the server has REVOKED and which has just been reduced to that
//     — is offered a restore (enter the code) or a clean start, because minting in that state would
//     strand the recoverable history;
//   * a device that has keys but no backup yet has one created automatically and is shown its
//     recovery code ONCE, since the code cannot be reproduced on a device that did not generate it.
//
// Both run off a post-frame callback so the surface underneath renders normally; the conversation
// list tolerates a not-yet-restored session (it shows previews from cache and defers decryption), so
// nothing here blocks the app — it only overlays the one prompt that matters.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/chat_providers.dart';
import '../chat/history_sync_controller.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import 'mls_errors.dart';
import 'mls_service.dart';
import '../widgets/adaptive/adaptive_feedback.dart';
import '../widgets/glass/glass.dart';

class RecoveryGate extends ConsumerStatefulWidget {
  const RecoveryGate({required this.child, super.key});

  final Widget child;

  @override
  ConsumerState<RecoveryGate> createState() => _RecoveryGateState();
}

class _RecoveryGateState extends ConsumerState<RecoveryGate> {
  bool _ran = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => unawaited(_run()));
  }

  Future<void> _run() async {
    if (_ran || !mounted) return;
    _ran = true;

    final userId = ref.read(myUserIdProvider);
    if (userId.isEmpty) return;
    final mls = ref.read(mlsServiceProvider);

    // Ask for the session FIRST, and let it be the one to say whether a restore is owed.
    //
    // This used to decide from local state alone — no keys on disk, no "start fresh" on record, a
    // backup on the server — and that question cannot be answered before the session has loaded,
    // because the load is the only thing that asks the server whether the keys on disk are still
    // ALIVE. A device revoked from elsewhere still holds its keys, so hasLocalKeys said yes, the
    // gate stood down, and the load it then triggered (via ensureRecoveryBackup) discarded the dead
    // identity, found the backup, and threw — into a `catch` that dropped it on the floor. No
    // prompt, and nothing could be sent or received for the rest of the run.
    //
    // load() throws NeedsRestoreException in exactly the cases this gate exists for: a fresh device
    // with a backup waiting, and a revoked one that has just been reduced to that. Both want the
    // same prompt.
    try {
      await mls.session(userId);
    } on NeedsRestoreException {
      if (mounted) await _promptRestore(userId, mls);
      return;
    } on Object {
      // Offline, or the session could not be built for some other reason — say nothing and let the
      // next launch retry. Nothing here blocks the chat.
      return;
    }

    // Otherwise, make sure this device has a backup and show a freshly generated code once.
    try {
      final code = await mls.ensureRecoveryBackup(userId);
      if (code != null && mounted) await _showCode(code);
    } on Object {
      // No keys yet, or offline; the next launch tries again. Never blocks the chat.
    }
  }

  /// The one-time "write this down" display. Not dismissible until acknowledged — the code cannot be
  /// shown again on a device that did not generate it.
  Future<void> _showCode(String code) {
    final l10n = AppLocalizations.of(context);
    return showGlassDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) {
        var acked = false;
        return StatefulBuilder(
          builder: (ctx, setState) => GlassDialog(
            title: Text(l10n.t('recovery.setupTitle')),
            content: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(l10n.t('recovery.setupDescription')),
                  const SizedBox(height: 16),
                  _CodeBox(code: code),
                  const SizedBox(height: 12),
                  Text(
                    l10n.t('recovery.setupWarning'),
                    style: Theme.of(ctx).textTheme.bodySmall,
                  ),
                  const SizedBox(height: 8),
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    controlAffinity: ListTileControlAffinity.leading,
                    value: acked,
                    onChanged: (v) => setState(() => acked = v ?? false),
                    title: Text(l10n.t('recovery.saved')),
                  ),
                ],
              ),
            ),
            actions: [
              GlassDialogAction(
                label: l10n.t('recovery.done'),
                emphasised: true,
                onPressed: acked ? () => Navigator.of(ctx).pop() : null,
              ),
            ],
          ),
        );
      },
    );
  }

  /// The restore prompt for a fresh device: enter the recovery code, or start clean.
  Future<void> _promptRestore(String userId, MlsService mls) {
    final l10n = AppLocalizations.of(context);
    final controller = TextEditingController();
    return showGlassDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) {
        var busy = false;
        return StatefulBuilder(
          builder: (ctx, setState) {
            Future<void> restore() async {
              final secret = controller.text.trim();
              if (busy || secret.isEmpty) return;
              setState(() => busy = true);
              try {
                final ok = await mls.restoreWithSecret(userId, secret);
                if (ctx.mounted) Navigator.of(ctx).pop();
                if (ok && mounted) {
                  ref.invalidate(conversationListProvider);
                  // Say what actually came back. A restore proves the code and brings the account
                  // back whether or not it recovers any history — the history rides in a separate
                  // seal — and reporting plain success for a restore that recovered nothing is how
                  // somebody ends up staring at unreadable messages with no idea why.
                  final outcome = mls.lastRestore;
                  if (outcome != null && outcome.historyMissing) {
                    notifyError(
                      context,
                      l10n.t(
                        outcome.transcriptError != null
                            ? 'backup.historyUnreadable'
                            : 'backup.historyAbsent',
                      ),
                    );
                  } else {
                    notifySuccess(context, l10n.t('backup.saved'));
                  }
                }
              } on IdentityAlreadySetUpException {
                if (ctx.mounted) Navigator.of(ctx).pop();
              } on Object {
                if (ctx.mounted) setState(() => busy = false);
                if (mounted) {
                  notifyError(context, l10n.t('backup.wrongPassphrase'));
                }
              }
            }

            Future<void> startFresh() async {
              await mls.acceptFreshIdentity();
              if (ctx.mounted) Navigator.of(ctx).pop();
            }

            return GlassDialog(
              title: Text(l10n.t('recovery.restoreTitle')),
              content: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(l10n.t('recovery.restoreDescription')),
                    const SizedBox(height: 16),
                    TextField(
                      controller: controller,
                      autofocus: true,
                      autocorrect: false,
                      enableSuggestions: false,
                      textCapitalization: TextCapitalization.characters,
                      decoration: InputDecoration(
                        labelText: l10n.t('recovery.codeLabel'),
                        hintText: l10n.t('recovery.codePlaceholder'),
                      ),
                      onSubmitted: (_) => restore(),
                    ),
                  ],
                ),
              ),
              actions: [
                GlassDialogAction(
                  label: l10n.t('backup.skip'),
                  onPressed: busy ? null : startFresh,
                ),
                GlassDialogAction(
                  label: l10n.t('backup.restore'),
                  emphasised: true,
                  onPressed: busy ? null : restore,
                ),
              ],
            );
          },
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    // Keep device-to-device history sync alive app-wide, so a co-member answers a new device's
    // history request whether or not anyone is looking at the conversation it concerns.
    ref.watch(historySyncControllerProvider);
    return widget.child;
  }
}

/// The recovery code, shown in a selectable monospace box with a copy button.
class _CodeBox extends StatelessWidget {
  const _CodeBox({required this.code});

  final String code;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final scheme = Theme.of(context).colorScheme;
    return Row(
      children: [
        Expanded(
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            decoration: BoxDecoration(
              color: scheme.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(8),
            ),
            child: SelectableText(
              code,
              style: const TextStyle(
                fontFamily: 'monospace',
                fontSize: 16,
                letterSpacing: 1.5,
              ),
            ),
          ),
        ),
        IconButton(
          icon: const Icon(Icons.copy),
          tooltip: l10n.t('recovery.copy'),
          onPressed: () async {
            await Clipboard.setData(ClipboardData(text: code));
            if (context.mounted) {
              notifySuccess(context, l10n.t('recovery.copied'));
            }
          },
        ),
      ],
    );
  }
}

/// Shows the stored recovery code again, or offers to generate a new one — the "view code" screen,
/// opened from settings. A restored device holds no local code; there we explain that and offer to
/// generate a fresh one, which re-seals the backup and retires the old code.
/// Asks for a recovery code, returning what was typed or null if cancelled.
///
/// Its own dialog rather than a field on the sheet: entering a code is a different act from reading
/// one, and mixing them on one surface invites typing a code into a screen that is showing one.
Future<String?> promptForRecoveryCode(BuildContext context) async {
  final l10n = AppLocalizations.of(context);
  final controller = TextEditingController();
  final entered = await showGlassDialog<String>(
    context: context,
    builder: (ctx) => GlassDialog(
      title: Text(l10n.t('recovery.restoreTitle')),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(l10n.t('recovery.restoreDescription')),
            const SizedBox(height: 16),
            TextField(
              controller: controller,
              autofocus: true,
              autocorrect: false,
              enableSuggestions: false,
              textCapitalization: TextCapitalization.characters,
              decoration: InputDecoration(
                labelText: l10n.t('recovery.codeLabel'),
                hintText: l10n.t('recovery.codePlaceholder'),
              ),
              onSubmitted: (v) => Navigator.of(ctx).pop(v.trim()),
            ),
          ],
        ),
      ),
      actions: [
        GlassDialogAction(
          label: l10n.t('common.cancel'),
          onPressed: () => Navigator.of(ctx).pop(),
        ),
        GlassDialogAction(
          label: l10n.t('recovery.restoreAction'),
          emphasised: true,
          onPressed: () => Navigator.of(ctx).pop(controller.text.trim()),
        ),
      ],
    ),
  );
  controller.dispose();
  return entered;
}

Future<void> showRecoveryCodeSheet(BuildContext context, WidgetRef ref) async {
  final l10n = AppLocalizations.of(context);
  final mls = ref.read(mlsServiceProvider);
  final userId = ref.read(myUserIdProvider);
  final code = await mls.recoveryCode();
  if (!context.mounted) return;

  await showGlassDialog<void>(
    context: context,
    builder: (ctx) {
      var busy = false;
      var shown = code;
      return StatefulBuilder(
        builder: (ctx, setState) {
          Future<void> regenerate() async {
            if (busy || userId.isEmpty) return;
            setState(() => busy = true);
            try {
              final next = await mls.regenerateRecoveryCode(userId);
              setState(() {
                shown = next;
                busy = false;
              });
              if (context.mounted) {
                notifySuccess(context, l10n.t('recovery.regenerated'));
              }
            } on Object {
              setState(() => busy = false);
              if (context.mounted) {
                notifyError(context, l10n.t('recovery.regenerateFailed'));
              }
            }
          }

          /// Restores from a code typed here, replacing this device's identity.
          ///
          /// Confirmed first, because it is not a small thing: this device gets a new identity, and
          /// anything it has read that is not in the backup and not in its own cache is not coming
          /// back. The code is proven against the backup before anything is touched, so a wrong one
          /// costs nothing.
          Future<void> restoreHere() async {
            if (busy || userId.isEmpty) return;
            final entered = await promptForRecoveryCode(context);
            if (entered == null || entered.isEmpty) return;
            if (!context.mounted) return;
            final ok = await showAdaptiveConfirm(
              context,
              title: l10n.t('recovery.restoreHere'),
              message: l10n.t('recovery.restoreHereConfirm'),
              confirmLabel: l10n.t('recovery.restoreHere'),
              cancelLabel: l10n.t('common.cancel'),
              isDestructive: true,
            );
            if (!ok) return;

            setState(() => busy = true);
            try {
              final restored = await mls.restoreWithSecret(
                userId,
                entered,
                replaceExisting: true,
              );
              if (ctx.mounted) Navigator.of(ctx).pop();
              if (!context.mounted) return;
              if (!restored) {
                notifyError(context, l10n.t('recovery.noBackup'));
                return;
              }
              ref.invalidate(conversationListProvider);
              final outcome = mls.lastRestore;
              if (outcome != null && outcome.historyMissing) {
                notifyError(
                  context,
                  l10n.t(
                    outcome.transcriptError != null
                        ? 'backup.historyUnreadable'
                        : 'backup.historyAbsent',
                  ),
                );
              } else {
                notifySuccess(context, l10n.t('backup.saved'));
              }
            } on Object {
              setState(() => busy = false);
              if (context.mounted) {
                notifyError(context, l10n.t('backup.wrongPassphrase'));
              }
            }
          }

          return GlassDialog(
            title: Text(l10n.t('recovery.viewTitle')),
            content: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (shown != null) ...[
                    Text(l10n.t('recovery.viewDescription')),
                    const SizedBox(height: 16),
                    _CodeBox(code: shown!),
                  ] else
                    Text(l10n.t('recovery.notOnThisDevice')),
                ],
              ),
            ),
            actions: [
              // The way back for somebody who declined the restore at first launch, or who is
              // holding a code from another device. Without it that was a one-time decision with no
              // route back: the gate only offers a restore to a device with no identity, and this
              // device has one.
              GlassDialogAction(
                label: l10n.t('recovery.restoreHere'),
                onPressed: busy ? null : restoreHere,
              ),
              GlassDialogAction(
                label: l10n.t('recovery.regenerate'),
                onPressed: busy ? null : regenerate,
              ),
              GlassDialogAction(
                label: l10n.t('recovery.done'),
                emphasised: true,
                onPressed: () => Navigator.of(ctx).pop(),
              ),
            ],
          );
        },
      );
    },
  );
}
