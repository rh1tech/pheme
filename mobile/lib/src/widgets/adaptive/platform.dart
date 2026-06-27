import 'package:flutter/material.dart';

/// True when the UI should render in the Cupertino (iOS) idiom.
///
/// Driven by [ThemeData.platform] so it honours
/// `debugDefaultTargetPlatformOverride` in tests and stays correct inside the
/// `MaterialApp` shell we keep for Android. The whole adaptive widget layer
/// branches on this single predicate so each screen stays declarative.
bool isCupertino(BuildContext context) {
  final platform = Theme.of(context).platform;
  return platform == TargetPlatform.iOS || platform == TargetPlatform.macOS;
}
