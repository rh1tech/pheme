import 'package:flutter/material.dart';

import '../l10n/app_localizations.dart';
import '../models/models.dart';

/// Small pill showing a channel's subscription mode (open / approval).
class ModeBadge extends StatelessWidget {
  const ModeBadge({super.key, required this.mode});

  final SubscriptionMode mode;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isOpen = mode == SubscriptionMode.open;
    final color = isOpen ? scheme.tertiary : scheme.primary;
    final label = context.l10n.t(isOpen ? 'mode.open' : 'mode.approval');
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
