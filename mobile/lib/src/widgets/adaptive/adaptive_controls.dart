import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'platform.dart';

/// Visual emphasis for [AdaptiveButton], mirroring the Material button trio.
enum AdaptiveButtonVariant { filled, text, outlined }

/// A button that renders a [CupertinoButton] on iOS and the matching Material
/// button ([FilledButton] / [TextButton] / [OutlinedButton]) on Android, so
/// each platform keeps its native press feedback and shape.
class AdaptiveButton extends StatelessWidget {
  const AdaptiveButton({
    super.key,
    required this.onPressed,
    required this.child,
    this.variant = AdaptiveButtonVariant.filled,
    this.isDestructive = false,
  });

  /// Primary emphasis (Material [FilledButton] / iOS filled button).
  const AdaptiveButton.filled({
    super.key,
    required this.onPressed,
    required this.child,
    this.isDestructive = false,
  }) : variant = AdaptiveButtonVariant.filled;

  /// Low emphasis (Material [TextButton] / iOS borderless button).
  const AdaptiveButton.text({
    super.key,
    required this.onPressed,
    required this.child,
    this.isDestructive = false,
  }) : variant = AdaptiveButtonVariant.text;

  /// Medium emphasis (Material [OutlinedButton] / iOS tinted button).
  const AdaptiveButton.outlined({
    super.key,
    required this.onPressed,
    required this.child,
    this.isDestructive = false,
  }) : variant = AdaptiveButtonVariant.outlined;

  final VoidCallback? onPressed;
  final Widget child;
  final AdaptiveButtonVariant variant;
  final bool isDestructive;

  @override
  Widget build(BuildContext context) {
    return isCupertino(context) ? _cupertino(context) : _material(context);
  }

  Widget _cupertino(BuildContext context) {
    final primary = CupertinoTheme.of(context).primaryColor;
    switch (variant) {
      case AdaptiveButtonVariant.filled:
        // Force a white foreground: a custom `color` doesn't always resolve the
        // theme's contrasting colour, which can leave the label invisible.
        return CupertinoButton(
          color: isDestructive ? CupertinoColors.destructiveRed : primary,
          onPressed: onPressed,
          child: DefaultTextStyle.merge(
            style: const TextStyle(color: CupertinoColors.white),
            child: IconTheme.merge(
              data: const IconThemeData(color: CupertinoColors.white),
              child: child,
            ),
          ),
        );
      case AdaptiveButtonVariant.outlined:
        // iOS has no outlined style; a tinted fill is the closest idiom.
        return CupertinoButton(
          color: (isDestructive ? CupertinoColors.destructiveRed : primary)
              .withValues(alpha: 0.12),
          onPressed: onPressed,
          child: DefaultTextStyle.merge(
            style: TextStyle(
              color: isDestructive ? CupertinoColors.destructiveRed : primary,
            ),
            child: IconTheme.merge(
              data: IconThemeData(
                color: isDestructive ? CupertinoColors.destructiveRed : primary,
              ),
              child: child,
            ),
          ),
        );
      case AdaptiveButtonVariant.text:
        return CupertinoButton(
          onPressed: onPressed,
          child: isDestructive
              ? DefaultTextStyle.merge(
                  style: const TextStyle(color: CupertinoColors.destructiveRed),
                  child: child,
                )
              : child,
        );
    }
  }

  Widget _material(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    switch (variant) {
      case AdaptiveButtonVariant.filled:
        return FilledButton(
          style: isDestructive
              ? FilledButton.styleFrom(
                  backgroundColor: scheme.error,
                  foregroundColor: scheme.onError,
                )
              : null,
          onPressed: onPressed,
          child: child,
        );
      case AdaptiveButtonVariant.outlined:
        return OutlinedButton(
          style: isDestructive
              ? OutlinedButton.styleFrom(foregroundColor: scheme.error)
              : null,
          onPressed: onPressed,
          child: child,
        );
      case AdaptiveButtonVariant.text:
        return TextButton(
          style: isDestructive
              ? TextButton.styleFrom(foregroundColor: scheme.error)
              : null,
          onPressed: onPressed,
          child: child,
        );
    }
  }
}

/// An icon-only button: a borderless [CupertinoButton] on iOS, [IconButton] on
/// Android. Sized for nav-bar/app-bar use.
class AdaptiveIconButton extends StatelessWidget {
  const AdaptiveIconButton({
    super.key,
    required this.icon,
    required this.onPressed,
    this.semanticLabel,
  });

  final IconData icon;
  final VoidCallback? onPressed;
  final String? semanticLabel;

  @override
  Widget build(BuildContext context) {
    if (isCupertino(context)) {
      return CupertinoButton(
        padding: EdgeInsets.zero,
        minimumSize: const Size.square(36),
        onPressed: onPressed,
        child: Icon(icon, semanticLabel: semanticLabel, size: 24),
      );
    }
    return IconButton(
      tooltip: semanticLabel,
      icon: Icon(icon),
      onPressed: onPressed,
    );
  }
}

/// A platform-native indeterminate progress indicator.
class AdaptiveProgress extends StatelessWidget {
  const AdaptiveProgress({super.key, this.size});

  /// Used to size the iOS [CupertinoActivityIndicator] / the box around the
  /// Material spinner. Defaults to each platform's natural size.
  final double? size;

  @override
  Widget build(BuildContext context) {
    if (isCupertino(context)) {
      return CupertinoActivityIndicator(radius: (size ?? 20) / 2);
    }
    final spinner = const CircularProgressIndicator(strokeWidth: 2);
    if (size == null) return spinner;
    return SizedBox(width: size, height: size, child: spinner);
  }
}
