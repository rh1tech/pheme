// The controls. One circle, one pill, one primary action — the same three on both platforms.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'glass_surface.dart';
import 'glass_tokens.dart';

/// Sinks a control slightly while it is held, and gives it a tap.
///
/// Press feedback used to be whatever each platform's button widget did — an ink splash on Android,
/// an opacity fade on iOS — which is exactly the kind of difference that made one build look like a
/// port of the other. A scale is compositor-only, reads identically on both, and is what the glass
/// controls we are copying actually do.
class _Pressable extends StatefulWidget {
  const _Pressable({
    required this.onPressed,
    required this.child,
    required this.semanticLabel,
  });

  final VoidCallback? onPressed;
  final Widget child;
  final String semanticLabel;

  @override
  State<_Pressable> createState() => _PressableState();
}

class _PressableState extends State<_Pressable> {
  bool _down = false;

  void _set(bool down) {
    if (_down != down) setState(() => _down = down);
  }

  @override
  Widget build(BuildContext context) {
    final enabled = widget.onPressed != null;

    return Semantics(
      button: true,
      enabled: enabled,
      label: widget.semanticLabel,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTapDown: enabled ? (_) => _set(true) : null,
        onTapUp: enabled ? (_) => _set(false) : null,
        onTapCancel: enabled ? () => _set(false) : null,
        onTap: enabled
            ? () {
                // A device with no haptic engine simply does nothing, and a failure to buzz is
                // never worth interrupting a tap for.
                unawaited(HapticFeedback.lightImpact());
                widget.onPressed!();
              }
            : null,
        child: AnimatedScale(
          scale: _down ? 0.90 : 1,
          duration: GlassMetrics.pressDuration,
          curve: Curves.easeOut,
          child: AnimatedOpacity(
            opacity: enabled ? 1 : 0.38,
            duration: GlassMetrics.pressDuration,
            child: ExcludeSemantics(child: widget.child),
          ),
        ),
      ),
    );
  }
}

/// A circular glass button — the app's bar button, and the control that floats over a photo.
///
/// The circle is [GlassMetrics.control] across; the touch target around it is padded out to
/// [GlassMetrics.minTapTarget] so the smaller disc costs nothing in reachability.
class GlassIconButton extends StatelessWidget {
  const GlassIconButton({
    super.key,
    required this.icon,
    required this.onPressed,
    required this.semanticLabel,
    this.tinted = false,
    this.destructive = false,
    this.badge = false,
  });

  final IconData icon;
  final VoidCallback? onPressed;
  final String semanticLabel;

  /// Fills the circle with the brand tint instead of glass — for the one control on a bar that is
  /// the point of the bar.
  final bool tinted;

  final bool destructive;

  /// A dot in the top-right corner, for a control carrying something unread.
  final bool badge;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final scheme = Theme.of(context).colorScheme;

    final foreground = destructive
        ? scheme.error
        : tinted
        ? scheme.onPrimary
        : palette.foreground;

    Widget circle = SizedBox.square(
      dimension: GlassMetrics.control,
      child: tinted
          ? DecoratedBox(
              decoration: BoxDecoration(
                color: palette.accent,
                shape: BoxShape.circle,
                boxShadow: palette.shadow,
              ),
              child: Icon(icon, size: GlassMetrics.icon, color: foreground),
            )
          : GlassSurface(
              borderRadius: BorderRadius.circular(GlassMetrics.control / 2),
              child: Icon(icon, size: GlassMetrics.icon, color: foreground),
            ),
    );

    if (badge) {
      circle = Stack(
        clipBehavior: Clip.none,
        children: [
          circle,
          Positioned(
            right: 0,
            top: 0,
            child: Container(
              width: 9,
              height: 9,
              decoration: BoxDecoration(
                color: scheme.primary,
                shape: BoxShape.circle,
                border: Border.all(color: scheme.surface, width: 1.5),
              ),
            ),
          ),
        ],
      );
    }

    return _Pressable(
      onPressed: onPressed,
      semanticLabel: semanticLabel,
      child: SizedBox.square(
        dimension: GlassMetrics.minTapTarget,
        child: Center(child: circle),
      ),
    );
  }
}

/// A glass pill with an icon and a label — a secondary action that needs a word to make sense.
class GlassPillButton extends StatelessWidget {
  const GlassPillButton({
    super.key,
    required this.icon,
    required this.label,
    required this.onPressed,
  });

  final IconData icon;
  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);

    return _Pressable(
      onPressed: onPressed,
      semanticLabel: label,
      child: GlassSurface(
        floating: true,
        borderRadius: BorderRadius.circular(GlassMetrics.control / 2),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: GlassMetrics.icon, color: palette.accent),
            const SizedBox(width: 8),
            Text(
              label,
              style: TextStyle(
                fontSize: 15,
                fontWeight: FontWeight.w600,
                color: palette.foreground,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The primary action of an ANDROID screen: a brand-filled circle floating in the bottom corner.
///
/// It replaces the Material extended FAB — same idea, same corner, cut from this design language
/// rather than from stock M3. iOS does not get one: a floating action button is a Material idiom
/// and there is a native place for the same action, which is last on the nav bar. That is the
/// platform difference being kept deliberately, not an inconsistency left behind.
class GlassActionButton extends StatelessWidget {
  const GlassActionButton({
    super.key,
    required this.icon,
    required this.semanticLabel,
    required this.onPressed,
  });

  final IconData icon;
  final String semanticLabel;
  final VoidCallback? onPressed;

  /// Diameter. Larger than a bar control: it is the one thing on the screen that should be findable
  /// without looking.
  static const double size = 54;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final scheme = Theme.of(context).colorScheme;

    return _Pressable(
      onPressed: onPressed,
      semanticLabel: semanticLabel,
      child: Container(
        width: size,
        height: size,
        decoration: BoxDecoration(
          color: scheme.primary,
          shape: BoxShape.circle,
          boxShadow: palette.shadow,
        ),
        child: Icon(icon, size: 26, color: scheme.onPrimary),
      ),
    );
  }
}
