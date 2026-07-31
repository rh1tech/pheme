// "Back up now" — the manual override for a backup that is otherwise automatic.
//
// Automatic backup is the thing that should normally make this button pointless: bodies are
// appended as they are written and the snapshot checkpoints behind them. But "should normally" is
// not a guarantee somebody can act on, and there are real moments where a person wants to KNOW
// rather than trust — about to wipe a phone, about to hand one over, about to travel, or simply
// having just read that a backup was failing and wanting to see it stop failing.
//
// Shared by the chat's verify sheet and the security settings page so the two cannot drift into
// disagreeing about what pressing it does.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../chat/chat_providers.dart';
import '../chat/chat_shield_status.dart';
import '../core/snackbar.dart';
import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive_controls.dart';

class BackupNowButton extends ConsumerStatefulWidget {
  const BackupNowButton({super.key, this.compact = false});

  /// A text button rather than a filled one, for sitting inside a sheet that already has a primary
  /// action of its own.
  final bool compact;

  @override
  ConsumerState<BackupNowButton> createState() => _BackupNowButtonState();
}

class _BackupNowButtonState extends ConsumerState<BackupNowButton> {
  bool _busy = false;

  Future<void> _run() async {
    if (_busy) return;
    setState(() => _busy = true);
    final l10n = AppLocalizations.of(context);
    final mls = ref.read(mlsServiceProvider);
    final userId = ref.read(myUserIdProvider);
    try {
      await mls.backUpNow(userId);
      if (mounted) notifySuccess(context, l10n.t('backup.nowDone'));
    } on Object {
      // Deliberately not the raw error. What reaches here is a Dio exception or a Rust panic
      // message, and neither tells somebody anything they can act on; the shield keeps the detail
      // for the state it reports, and this says the one thing that matters — it did not work.
      if (mounted) notifyError(context, l10n.t('backup.nowFailed'));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context);
    final backup = ref.watch(backupHealthProvider);

    // With no recovery code there is nothing to seal under, so the button cannot do its job. Shown
    // disabled rather than hidden: an absent control reads as "this is fine", and a device backing
    // nothing up is the least fine state there is — the surrounding text says so, and this makes
    // it obvious that pressing something is not the answer.
    final enabled = backup.armed && !_busy;

    final label = Text(
      _busy ? l10n.t('backup.nowRunning') : l10n.t('backup.now'),
    );

    if (widget.compact) {
      return Align(
        alignment: Alignment.centerLeft,
        child: AdaptiveButton.text(
          onPressed: enabled ? _run : null,
          child: label,
        ),
      );
    }
    return AdaptiveButton.filled(
      onPressed: enabled ? _run : null,
      child: label,
    );
  }
}
