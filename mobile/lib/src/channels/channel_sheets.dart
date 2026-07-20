import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';

/// Hosts one of the channel's management surfaces in a sheet.
///
/// These were tabs — Send, Subscribers, Keys, Settings — sitting alongside the messages, so a
/// channel opened onto a tab bar where a chat opens onto its messages. Four of the five tabs were
/// things you do to a channel occasionally; one was the channel itself. Giving them equal billing
/// put the reason you opened the screen behind a row of things you mostly did not want.
///
/// They are sheets now, reached from the ⋮ menu, which is where a chat keeps the same kind of
/// action. The widgets inside are unchanged: they were already self-contained, and rewriting them
/// to prove the point would have risked working code for no gain.
Future<T?> showChannelSheet<T>(
  BuildContext context, {
  required String title,
  required Widget child,
}) {
  return showModalBottomSheet<T>(
    context: context,
    isScrollControlled: true,
    // Without this the sheet runs under the status bar and the notch on a tall screen — the same
    // reason the new-chat sheet asks for it.
    useSafeArea: true,
    showDragHandle: true,
    builder: (context) => FractionallySizedBox(
      heightFactor: 0.92,
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 4, 12, 8),
            child: Row(
              children: [
                Expanded(
                  child: Text(
                    title,
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close),
                  tooltip: context.l10n.t('common.close'),
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(child: child),
        ],
      ),
    ),
  );
}
