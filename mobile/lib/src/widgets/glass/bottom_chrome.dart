import 'package:flutter/widgets.dart';

/// How much floating chrome the shell is drawing over the bottom of a page.
///
/// Pages learn about the floating tab bar through their bottom PADDING, which is the right channel
/// for a scroll view: the list spends it as content padding and the last row comes to rest clear of
/// the bar. And when the keyboard is up the shell deliberately zeroes it, because there the screen
/// is already short and a bar's height of blank space is what made a focused search look like it had
/// cut the list off — the rows are meant to run under the glass.
///
/// But padding was carrying two different questions at once, and they have different answers.
/// "How much space should the last row leave?" is zero with the keyboard up. "How far off the bottom
/// must the floating action button sit?" is never zero, because the tab bar is still there — it rode
/// up with the keyboard rather than going away. Answering the second with the first put the pencil
/// and the plus entirely behind the tab bar the moment the search field took focus: still drawn,
/// still tappable in principle, and completely invisible under the bar painted on top of them.
///
/// So the shell states the chrome's extent here as well, and whatever needs to clear it — rather
/// than merely to avoid ending underneath it — asks this instead of reading the padding.
class BottomChrome extends InheritedWidget {
  const BottomChrome({super.key, required this.extent, required super.child});

  /// The height of the floating chrome at the bottom of the screen, in logical pixels.
  final double extent;

  /// What the shell is drawing over this page's bottom edge. Zero outside a shell — a pushed page
  /// has no tab bar under it, and its action button needs to clear nothing but the home indicator.
  static double of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<BottomChrome>()?.extent ?? 0;

  @override
  bool updateShouldNotify(BottomChrome oldWidget) => extent != oldWidget.extent;
}
