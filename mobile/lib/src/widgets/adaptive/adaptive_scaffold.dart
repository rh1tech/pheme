import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'platform.dart';

/// A page scaffold that renders the platform-native chrome: a
/// [CupertinoPageScaffold] + [CupertinoNavigationBar] on iOS and a
/// [Scaffold] + [AppBar] on Android, from one declarative API.
///
/// [trailing] maps to the nav-bar trailing slot on iOS and to the app-bar
/// `actions` on Android. [floatingActionButton] is Android-only — on iOS a
/// primary action belongs in [trailing].
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
    this.transitionBetweenRoutes = true,
  });

  final Widget body;
  final Widget? title;
  final Widget? leading;
  final List<Widget> trailing;
  final Widget? floatingActionButton;
  final bool automaticallyImplyLeading;
  final bool resizeToAvoidBottomInset;

  /// Whether this page's iOS nav bar animates into the next page's during a route
  /// push. Leave true for a page that is pushed; set FALSE for a page that lives in
  /// a tab alongside its siblings.
  ///
  /// [CupertinoNavigationBar] implements that animation with a [Hero], and when
  /// enabled every bar shares one default tag. Two bars alive at once under the same
  /// navigator therefore collide — "There are multiple heroes that share the same tag
  /// within a subtree" — which is exactly what home_shell's [IndexedStack] does: it
  /// keeps BOTH tab pages mounted, deliberately, so each keeps its scroll position.
  /// The tabs are not routes and have no transition to take part in, so they opt out.
  final bool transitionBetweenRoutes;

  /// When true, iOS fills the page with the grouped-table background
  /// (`systemGroupedBackground`) so inset list sections sit on grey, matching
  /// the native Settings-style look. No effect on Android.
  final bool grouped;

  @override
  Widget build(BuildContext context) {
    if (isCupertino(context)) {
      return CupertinoPageScaffold(
        resizeToAvoidBottomInset: resizeToAvoidBottomInset,
        backgroundColor: grouped
            ? CupertinoColors.systemGroupedBackground.resolveFrom(context)
            : null,
        navigationBar: CupertinoNavigationBar(
          automaticallyImplyLeading: automaticallyImplyLeading,
          transitionBetweenRoutes: transitionBetweenRoutes,
          leading: leading,
          middle: title,
          trailing: trailing.isEmpty
              ? null
              : Row(mainAxisSize: MainAxisSize.min, children: trailing),
          // An opaque background makes CupertinoPageScaffold inset the child
          // below the bar (rather than letting it scroll behind a blur).
          backgroundColor: CupertinoTheme.of(context).scaffoldBackgroundColor,
        ),
        // Under MaterialApp, CupertinoPageScaffold provides no DefaultTextStyle,
        // so raw Text would fall back to the red/underlined error style. Seed
        // the Cupertino label style for the whole body.
        child: DefaultTextStyle(
          style: CupertinoTheme.of(context).textTheme.textStyle,
          // And it provides no Material ancestor either, which is the other half of the
          // same problem. ListTile, Card, InkWell and friends assert on one and throw
          // "No Material widget found" — a full-screen red error, not a degraded layout.
          //
          // That is what "Devices & security" was on iOS: security_page.dart builds its
          // rows with ListTile, found no Material above it, and rendered the error box
          // instead of the page. It is not alone — settings, chat, conversations and
          // forgot-password all put Material widgets inside this scaffold, so the fix
          // belongs here rather than in any one of them.
          //
          // MaterialType.transparency paints nothing, so the Cupertino background (plain
          // or `grouped`) still shows through and nothing about the iOS look changes.
          child: Material(
            type: MaterialType.transparency,
            child: SafeArea(top: false, child: body),
          ),
        ),
      );
    }
    return Scaffold(
      resizeToAvoidBottomInset: resizeToAvoidBottomInset,
      appBar: AppBar(
        automaticallyImplyLeading: automaticallyImplyLeading,
        leading: leading,
        title: title,
        actions: trailing.isEmpty ? null : trailing,
      ),
      floatingActionButton: floatingActionButton,
      // The iOS branch above has always wrapped its body; this one did not, and under the
      // edge-to-edge enforcement that Flutter applies on Android the last rows of any scrolling
      // page render underneath the system gesture bar. On Settings that is the Logout button,
      // which is what "the settings screen is truncated" was.
      //
      // top: false because the AppBar already handles the status bar — adding it here would inset
      // the body twice.
      body: SafeArea(top: false, child: body),
    );
  }
}
