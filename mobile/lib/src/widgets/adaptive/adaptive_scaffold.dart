import 'package:flutter/material.dart';

import '../glass/glass.dart';

/// A page scaffold with the app's glass chrome: a blurred bar across the top, the body free to run
/// underneath it, and a single place where a screen's primary action lives.
///
/// It used to build two entirely different pages — a [CupertinoPageScaffold] with a
/// [CupertinoNavigationBar] on iOS, a Material [Scaffold] with an [AppBar] on Android — and that is
/// where the two builds diverged. Not in the bars alone: the Cupertino branch brought its own text
/// defaults, its own missing Material ancestor, its own Hero tags to collide, and its own idea of
/// where a primary action goes (nav bar on one, floating button on the other). Every screen then
/// papered over those differences its own way, with its own paddings.
///
/// Now there is one page. The platform differences that remain are navigational and live one level
/// down, in [GlassAppBar] (back chevron vs back arrow, centred vs leading title) and in the page
/// transition, which [MaterialApp] still picks per platform — so an iOS build keeps its
/// swipe-back and its horizontal slide, and Android keeps its own.
class AdaptiveScaffold extends StatelessWidget {
  const AdaptiveScaffold({
    super.key,
    required this.body,
    this.title,
    this.leading,
    this.trailing = const [],
    this.floatingActionButton,
    this.automaticallyImplyLeading = true,
    this.resizeToAvoidBottomInset = true,
    this.grouped = false,
    this.centerTitle,
    this.behindChrome = false,
    this.backgroundColor,
  });

  final Widget body;
  final Widget? title;
  final Widget? leading;

  /// The bar's trailing controls. [GlassIconButton]s, in the order they should read left to right.
  final List<Widget> trailing;

  /// The screen's primary action, floating in the bottom-trailing corner.
  ///
  /// ANDROID ONLY, by convention rather than by enforcement: iOS has no floating action button, and
  /// one imported from Material looks imported. An iOS screen puts the same action last on the bar
  /// instead, as a [GlassIconButton] in [trailing]. Callers make that choice — see
  /// conversations_page.dart, which does both.
  final Widget? floatingActionButton;

  final bool automaticallyImplyLeading;
  final bool resizeToAvoidBottomInset;

  /// Whether the page uses the grouped (inset-list) background rather than the plain one.
  final bool grouped;

  /// Overrides the platform's title alignment. See [GlassAppBar.centerTitle].
  final bool? centerTitle;

  /// Whether the body draws behind the top bar and the bottom chrome.
  ///
  /// When true — which is what every list and feed in the app wants — the body is handed a
  /// [MediaQuery] whose padding already includes the bar, and it is the body's job to spend that on
  /// its scroll view's content padding so the content scrolls *under* the glass rather than
  /// starting below it. [AdaptiveRefreshableScrollView] does that automatically.
  ///
  /// When false, the body is simply inset below the bar, which is what a form or a settings list
  /// wants: there is nothing to see behind the glass on a page that does not scroll far.
  final bool behindChrome;

  final Color? backgroundColor;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    final hasBar =
        title != null ||
        leading != null ||
        trailing.isNotEmpty ||
        (automaticallyImplyLeading &&
            (ModalRoute.of(context)?.impliesAppBarDismissal ?? false));

    return Scaffold(
      backgroundColor:
          backgroundColor ??
          (grouped ? scheme.surfaceContainerLow : scheme.surface),
      resizeToAvoidBottomInset: resizeToAvoidBottomInset,
      // A Builder, so the MediaQuery read below is the one SCAFFOLD provides rather than the one
      // above it. The difference only shows when the keyboard is up: the scaffold shrinks the body
      // and zeroes the bottom padding it has just consumed, and reading from outside re-added the
      // home indicator's inset on top of the keyboard — which pushed the last rows of every list
      // off the bottom of a viewport that had already shrunk to make room.
      body: Builder(
        builder: (context) {
          final media = MediaQuery.of(context);

          // The body is told about the chrome by way of its padding, which is the same channel the
          // system uses for the status bar and the home indicator — so a scroll view that already
          // knows how to respect one needs no new concept to respect the other. The floating action
          // button is part of that: without clearance, the last row ends up underneath it.
          final inset = media.padding.copyWith(
            top: hasBar ? media.padding.top + GlassMetrics.barHeight : null,
            bottom: floatingActionButton == null
                ? null
                : media.padding.bottom +
                      GlassActionButton.size +
                      GlassMetrics.gutter,
          );

          // The SafeArea goes INSIDE the override, not around it.
          //
          // Around it, it insets by the ambient padding — the status bar — and the bar's own 52pt,
          // which only the override knows about, is never consumed by anything. Every page that
          // does not scroll behind the chrome then began 52pt too high, with its first line of text
          // behind the glass: the profile screen lost the sentence explaining what the screen is
          // for, and nothing about it looked broken enough to notice.
          final content = MediaQuery(
            data: media.copyWith(padding: inset),
            child: behindChrome ? body : SafeArea(child: body),
          );

          return Stack(
            children: [
              Positioned.fill(child: content),
              if (hasBar)
                Positioned(
                  top: 0,
                  left: 0,
                  right: 0,
                  child: GlassAppBar(
                    title: title,
                    leading: leading,
                    actions: trailing,
                    automaticallyImplyLeading: automaticallyImplyLeading,
                    centerTitle: centerTitle,
                  ),
                ),
              if (floatingActionButton != null)
                Positioned(
                  right: GlassMetrics.gutter,
                  // Above whatever the page already reserves at the bottom — the home indicator on
                  // a pushed page, the home indicator PLUS the floating tab bar on a tab.
                  bottom: media.padding.bottom + GlassMetrics.gutter,
                  child: floatingActionButton!,
                ),
            ],
          );
        },
      ),
    );
  }
}
