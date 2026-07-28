// The one widget that actually makes glass.

import 'dart:ui' show ImageFilter;

import 'package:flutter/material.dart';

import 'glass_tokens.dart';

/// A translucent, blurred surface: the material every bar, tab bar and floating control in the app
/// is cut from.
///
/// Three layers, in the order they are painted: the blur of whatever is behind, a translucent fill
/// over it, and a half-pixel edge. The blur is what makes content scrolling underneath read as
/// *underneath* rather than as clutter, and the edge is what stops the whole thing disappearing
/// when the content behind happens to be white.
///
/// The blur is dropped entirely when the user has asked for higher contrast — see
/// [GlassPalette.opaque]. A [BackdropFilter] is also the most expensive thing on any of these
/// screens, so surfaces are counted, not sprinkled: a top bar, a tab bar and a composer, never one
/// per row.
class GlassSurface extends StatelessWidget {
  const GlassSurface({
    super.key,
    required this.child,
    this.borderRadius,
    this.border,
    this.padding,
    this.floating = false,
  });

  final Widget child;

  /// Defaults to a fully rounded [GlassMetrics.barRadius] pill corner.
  final BorderRadiusGeometry? borderRadius;

  /// The hairline. Defaults to an edge all the way round; a bar flush against the top of the screen
  /// passes a bottom-only border instead, since its other three edges are off-screen.
  final BoxBorder? border;

  final EdgeInsetsGeometry? padding;

  /// Whether the surface floats above the content (and so casts a shadow) rather than sitting flush
  /// against an edge of the screen.
  final bool floating;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final shape = borderRadius ?? BorderRadius.circular(GlassMetrics.barRadius);
    final edge = border ?? Border.all(color: palette.hairline, width: 0.5);

    Widget surface = DecoratedBox(
      decoration: BoxDecoration(
        color: palette.fill,
        borderRadius: shape,
        border: edge,
      ),
      child: padding == null ? child : Padding(padding: padding!, child: child),
    );

    if (!palette.opaque) {
      surface = BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: GlassMetrics.blurSigma,
          sigmaY: GlassMetrics.blurSigma,
        ),
        child: surface,
      );
    }

    // The clip has to come outside the filter: a BackdropFilter samples the whole layer it is in,
    // so without it the blur leaks past the rounded corners as a square halo.
    surface = ClipRRect(borderRadius: shape, child: surface);

    if (!floating) return surface;
    return DecoratedBox(
      decoration: BoxDecoration(borderRadius: shape, boxShadow: palette.shadow),
      child: surface,
    );
  }
}
