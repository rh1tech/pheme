import 'package:flutter/material.dart';

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

/// A plain Material 3 theme seeded with the brand color. We deliberately do not
/// override component shapes/borders — relying on stock M3 keeps the UI looking
/// native on Android (and correct on iOS).
ThemeData buildTheme(Brightness brightness) {
  return ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(seedColor: kIris, brightness: brightness),
  );
}

final ThemeData lightTheme = buildTheme(Brightness.light);
final ThemeData darkTheme = buildTheme(Brightness.dark);
