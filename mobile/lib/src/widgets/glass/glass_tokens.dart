// The numbers and the material behind the app's chrome.
//
// Every bar, tab bar and floating control in the app is built from the same two things: these
// metrics and this palette. That is the whole point — the reason the iOS build looked unrelated to
// the Android one was that each screen chose its own paddings, its own icon sizes and its own idea
// of where a primary action lives. One source for the geometry means the two platforms cannot drift
// apart by accident again, and the places where they SHOULD differ (back chevron vs back arrow,
// centred vs leading title) stay deliberate and few.

import 'package:flutter/material.dart';

/// The geometry of the chrome. Fixed values, not derived from the text scale: these are the
/// dimensions of a control surface, and a bar that grows with the body font stops being a bar.
abstract final class GlassMetrics {
  /// Diameter of a standalone circular control — a bar button, a close button over a photo.
  ///
  /// 40 rather than the Material default 48: the visible circle is the button, and a 48pt disc
  /// beside a 26pt logo looks like a mistake. The touch target is padded back out to 44 by
  /// [minTapTarget], so the smaller circle costs nothing in reachability.
  static const double control = 40;

  /// The icon inside a [control].
  static const double icon = 20;

  /// Minimum side of the invisible touch target around any control (Apple HIG / Material both 44+).
  static const double minTapTarget = 44;

  /// Between two neighbouring controls in a bar.
  static const double gap = 8;

  /// Between the chrome and the edge of the screen, and between stacked floating pieces.
  static const double gutter = 12;

  /// Height of the top bar's content row, above the status-bar inset.
  static const double barHeight = 52;

  /// Height of the floating tab bar's content row, above the home-indicator inset.
  static const double tabBarHeight = 56;

  /// Corner radius of a floating bar or pill.
  static const double barRadius = 22;

  /// Corner radius of an inset card or a composer field.
  static const double fieldRadius = 20;

  /// Blur applied behind a glass surface. Telegram's own bars sit around here — enough that text
  /// scrolling under a bar becomes an unreadable wash, not so much that the colour underneath is
  /// lost.
  static const double blurSigma = 20;

  /// How long a press takes to sink in, and to come back.
  static const Duration pressDuration = Duration(milliseconds: 90);

  /// Total height a top bar occupies, including the status bar it sits under.
  static double barExtent(BuildContext context) =>
      MediaQuery.paddingOf(context).top + barHeight;

  /// Total height the floating tab bar occupies, including its gutter and the home indicator.
  static double tabBarExtent(BuildContext context) =>
      MediaQuery.paddingOf(context).bottom + tabBarHeight + gutter;
}

/// The colours of a glass surface, resolved against the ambient theme.
@immutable
class GlassPalette {
  const GlassPalette({
    required this.fill,
    required this.hairline,
    required this.shadow,
    required this.opaque,
    required this.foreground,
    required this.mutedForeground,
    required this.accent,
  });

  /// The translucent (or, when [opaque], solid) surface colour.
  final Color fill;

  /// The half-pixel edge that keeps a glass surface from dissolving into a light background.
  final Color hairline;

  /// Cast by a surface that floats above the content rather than sitting flush against an edge.
  final List<BoxShadow> shadow;

  /// True when the blur has been dropped and [fill] is solid.
  ///
  /// Flutter does not surface iOS's "Reduce Transparency" switch directly, but the platforms report
  /// the neighbouring "Increase Contrast" preference through [MediaQueryData.highContrast], and a
  /// user who asked for either is asking for the same thing here: text that is not competing with
  /// whatever happens to be scrolling underneath it.
  final bool opaque;

  final Color foreground;
  final Color mutedForeground;

  /// The brand tint, for a selected tab or a primary action.
  final Color accent;

  factory GlassPalette.of(BuildContext context) {
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final dark = theme.brightness == Brightness.dark;
    final opaque = MediaQuery.highContrastOf(context);

    // A hint of the brand violet in the glass itself, so the chrome belongs to Pheme rather than
    // reading as a stock system bar. Kept low: at anything above a few percent it stops looking
    // like glass and starts looking like a purple bar.
    final base = Color.alphaBlend(
      scheme.primary.withValues(alpha: dark ? 0.10 : 0.05),
      dark ? scheme.surfaceContainer : scheme.surface,
    );

    return GlassPalette(
      fill: opaque ? base : base.withValues(alpha: dark ? 0.62 : 0.74),
      hairline: dark
          ? Colors.white.withValues(alpha: 0.10)
          : Colors.black.withValues(alpha: 0.07),
      shadow: [
        BoxShadow(
          color: Colors.black.withValues(alpha: dark ? 0.44 : 0.13),
          blurRadius: 24,
          offset: const Offset(0, 8),
        ),
      ],
      opaque: opaque,
      foreground: scheme.onSurface,
      mutedForeground: scheme.onSurfaceVariant,
      accent: scheme.primary,
    );
  }
}
