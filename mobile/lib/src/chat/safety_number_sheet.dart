// The safety number: the digits two people compare, out of band, to prove nobody is in the middle.
//
// It is computed from the group's own ratchet tree, never from anything the server says — so a key
// the server swapped in shows up here as different digits. That is the entire point, and it is why
// the instruction below insists on comparing them somewhere OTHER than this chat: a server that could
// substitute a key could also rewrite the message in which you sent the number.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../l10n/app_localizations.dart';
import '../widgets/adaptive/adaptive_controls.dart';
import '../crypto/mls_service.dart';
import 'chat_shield_status.dart';
import 'safety_pin_store.dart';

Future<void> showSafetyNumberSheet(
  BuildContext context,
  String conversationId,
) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => _SafetyNumberSheet(conversationId: conversationId),
  );
}

class _SafetyNumberSheet extends ConsumerWidget {
  const _SafetyNumberSheet({required this.conversationId});

  final String conversationId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l10n = AppLocalizations.of(context);
    final theme = Theme.of(context);
    final safety = ref.watch(safetyNumberProvider(conversationId));

    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 4, 20, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              l10n.t('safety.title'),
              style: theme.textTheme.titleMedium?.copyWith(
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 16),
            safety.when(
              loading: () => const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: AdaptiveProgress()),
              ),
              error: (_, _) => Text(
                l10n.t('safety.notReady'),
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              data: (state) => _Body(
                state: state,
                conversationId: conversationId,
                l10n: l10n,
                theme: theme,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Body extends ConsumerWidget {
  const _Body({
    required this.state,
    required this.conversationId,
    required this.l10n,
    required this.theme,
  });

  final SafetyNumberState state;
  final String conversationId;
  final AppLocalizations l10n;
  final ThemeData theme;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // The group is not established, or this device has not been admitted to it. There is no number to
    // show, and inventing one would be worse than saying so.
    if (state.number.isEmpty) {
      return Text(
        l10n.t('safety.notReady'),
        style: theme.textTheme.bodySmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      );
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (state.changed)
          Container(
            margin: const EdgeInsets.only(bottom: 16),
            padding: const EdgeInsets.all(12),
            decoration: BoxDecoration(
              color: theme.colorScheme.errorContainer,
              borderRadius: BorderRadius.circular(12),
            ),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(
                  Icons.warning_amber_rounded,
                  size: 18,
                  color: theme.colorScheme.onErrorContainer,
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    l10n.t('safety.changed'),
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onErrorContainer,
                    ),
                  ),
                ),
              ],
            ),
          ),

        Text(
          l10n.t('safety.description'),
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 16),

        Container(
          padding: const EdgeInsets.symmetric(vertical: 16, horizontal: 12),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(12),
          ),
          child: Text(
            state.number,
            textAlign: TextAlign.center,
            style: theme.textTheme.titleMedium?.copyWith(
              fontFeatures: const [FontFeature.tabularFigures()],
              letterSpacing: 2,
              height: 1.8,
            ),
          ),
        ),
        const SizedBox(height: 16),

        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              Icons.verified_user_outlined,
              size: 18,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                l10n.t('safety.howTo'),
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ),

        // What the shield's colour is about, said in words. The number above answers "is this the
        // right person"; this answers "would I lose what I wrote if this phone were gone", which is
        // the question the backup exists for and the one nothing used to answer anywhere.
        const SizedBox(height: 20),
        _BackupStatus(conversationId: conversationId),

        if (state.changed) ...[
          const SizedBox(height: 20),
          AdaptiveButton.filled(
            onPressed: () async {
              await ref
                  .read(safetyPinStoreProvider)
                  .pin(conversationId, state.number);
              ref.invalidate(safetyNumberProvider(conversationId));
              if (context.mounted) Navigator.of(context).pop();
            },
            child: Text(l10n.t('safety.accept')),
          ),
        ],
      ],
    );
  }
}

/// The backup half of the shield: whether this device's history is off the handset, and if not,
/// why not.
class _BackupStatus extends ConsumerWidget {
  const _BackupStatus({required this.conversationId});

  final String conversationId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context);
    final shield = ref.watch(shieldStatusProvider(conversationId));
    final backup = shield.backup;

    // Ordered most-serious first, and each one says what it means rather than naming a mechanism.
    // "Auto-backup dormant" is a fact about the code; "nothing here is backed up" is a fact about
    // the person's messages, and only the second one lets them decide whether to care.
    final (icon, key, tone) = switch (backup) {
      BackupHealth(armed: false) => (
        Icons.gpp_maybe_outlined,
        'backup.statusNone',
        theme.colorScheme.error,
      ),
      BackupHealth(failing: true) => (
        Icons.sync_problem_outlined,
        'backup.statusFailing',
        theme.colorScheme.error,
      ),
      BackupHealth(pending: > 0) => (
        Icons.cloud_upload_outlined,
        'backup.statusPending',
        theme.colorScheme.tertiary,
      ),
      _ => (
        Icons.cloud_done_outlined,
        'backup.statusSafe',
        theme.colorScheme.onSurfaceVariant,
      ),
    };

    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(icon, size: 18, color: tone),
        const SizedBox(width: 8),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                l10n.t(key),
                style: theme.textTheme.bodySmall?.copyWith(color: tone),
              ),
              // The count, only when there is one. A silent "0 waiting" is noise; a number that
              // sits there and does not fall is the thing worth seeing.
              if (backup.pending > 0)
                Text(
                  l10n
                      .t('backup.statusPendingCount')
                      .replaceFirst('{count}', '${backup.pending}'),
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}
