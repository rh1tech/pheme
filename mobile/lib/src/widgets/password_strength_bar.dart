import 'package:flutter/material.dart';

import '../core/validators.dart';
import '../l10n/app_localizations.dart';

/// A compact password strength meter shown beneath a password field.
class PasswordStrengthBar extends StatelessWidget {
  const PasswordStrengthBar({super.key, required this.password});

  final String password;

  @override
  Widget build(BuildContext context) {
    if (password.isEmpty) return const SizedBox.shrink();
    final l10n = context.l10n;
    final score = passwordScore(password);
    const colors = [
      Colors.red,
      Colors.red,
      Colors.orange,
      Colors.lightGreen,
      Colors.green,
    ];
    final labels = [
      l10n.t('auth.strengthWeak'),
      l10n.t('auth.strengthWeak'),
      l10n.t('auth.strengthFair'),
      l10n.t('auth.strengthGood'),
      l10n.t('auth.strengthStrong'),
    ];
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(4),
            child: LinearProgressIndicator(
              value: (score + 1) / 5,
              minHeight: 4,
              backgroundColor: Theme.of(
                context,
              ).colorScheme.surfaceContainerHighest,
              valueColor: AlwaysStoppedAnimation<Color>(colors[score]),
            ),
          ),
          const SizedBox(height: 4),
          Text(
            '${l10n.t('auth.passwordStrength')}: ${labels[score]}',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
    );
  }
}
