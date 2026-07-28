import 'package:flutter/material.dart';

import 'widgets/glass/glass_tokens.dart';

/// Brand palette mirroring the web app's "Iris" violet identity (web/src/theme.ts).
/// iris.6 (#7740ee) seeds the Material 3 color scheme so the app uses standard
/// Android (Material You) components tinted to the Pheme brand.
const Color kIris = Color(0xFF7740EE);
// grape.6 — matches the web brand gradient (web/src/theme.ts / favicon.svg).
const Color kGrape = Color(0xFFBE4BDB);

/// The diagonal violet→grape brand gradient (matches BRAND_GRADIENT, 135°),
/// used only on the brand mark — the rest of the UI uses native M3 surfaces.
const LinearGradient kBrandGradient = LinearGradient(
  colors: [kIris, kGrape],
  begin: Alignment.topLeft,
  end: Alignment.bottomRight,
);

/// The Material 3 theme, seeded with the brand colour, plus the few component shapes the app's
/// chrome depends on.
///
/// It used to override nothing at all, on the theory that stock M3 keeps the UI native on Android.
/// That held while Android was the only platform drawing Material — but the menus, sheets and
/// dialogs are now the SAME widgets on both, so their corner radii are part of the shared design
/// language rather than a platform default to be left alone. They are pinned here, once, against
/// [GlassMetrics] so a sheet and the bar above it agree about what a rounded corner is.
///
/// Everything else is still stock: this is a handful of shapes, not a re-skin.
ThemeData buildTheme(Brightness brightness) {
  final scheme = ColorScheme.fromSeed(seedColor: kIris, brightness: brightness);

  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: scheme.surface,
    // No popupMenuTheme: nothing builds a Material menu any more. Every menu in the app is a
    // GlassMenuButton, which is glass rather than an opaque card — theming PopupMenuButton here
    // would be configuring a widget that is no longer used, which is worse than not configuring it.
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: scheme.surfaceContainerLow,
      showDragHandle: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
      ),
    ),
    dialogTheme: DialogThemeData(
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(GlassMetrics.fieldRadius),
      ),
    ),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      insetPadding: const EdgeInsets.all(GlassMetrics.gutter),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(GlassMetrics.fieldRadius),
      ),
    ),
  );
}

final ThemeData lightTheme = buildTheme(Brightness.light);
final ThemeData darkTheme = buildTheme(Brightness.dark);
