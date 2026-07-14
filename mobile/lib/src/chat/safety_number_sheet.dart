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
import 'chat_providers.dart';
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
