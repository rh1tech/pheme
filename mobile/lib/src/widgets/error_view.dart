import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import 'adaptive/adaptive.dart';

/// Centered error state with a retry action, used for failed async loads.
class ErrorView extends StatelessWidget {
  const ErrorView({super.key, required this.message, this.onRetry});

  final String message;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final cupertino = isCupertino(context);
    final icon = cupertino
        ? CupertinoIcons.wifi_slash
        : Icons.cloud_off_rounded;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 40, color: scheme.outline),
            const SizedBox(height: 12),
            Text(
              message,
              textAlign: TextAlign.center,
              style: TextStyle(color: scheme.onSurfaceVariant),
            ),
            if (onRetry != null) ...[
              const SizedBox(height: 16),
              AdaptiveButton.outlined(
                onPressed: onRetry,
                child: Text(context.l10n.t('common.retry')),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
