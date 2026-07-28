// The top bar, on both platforms.

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../adaptive/platform.dart';
import 'glass_button.dart';
import 'glass_surface.dart';
import 'glass_tokens.dart';

/// A glass bar across the top of a screen, with the content free to scroll underneath it.
///
/// This replaces both the [AppBar] Android used and the [CupertinoNavigationBar] iOS used, which is
/// the whole reason the two builds looked like different products: two bars with different heights,
/// different button sizes, different title weights and different paddings, agreeing on nothing but
/// the words in them. One bar, one set of metrics.
///
/// What stays per-platform is navigation, not decoration:
///
///   * the back affordance is a chevron on iOS and an arrow on Android, because that is what "back"
///     looks like to each platform's users;
///   * the title is centred on iOS and leading-aligned on Android, which is the single strongest
///     layout convention either platform has.
///
/// Everything else — the material, the 40pt circular buttons, the 8pt gaps, the 12pt gutter, the
/// 52pt row — is identical.
class GlassAppBar extends StatelessWidget {
  const GlassAppBar({
    super.key,
    this.title,
    this.leading,
    this.actions = const [],
    this.automaticallyImplyLeading = true,
    this.centerTitle,
  });

  final Widget? title;
  final Widget? leading;
  final List<Widget> actions;
  final bool automaticallyImplyLeading;

  /// Defaults to the platform convention. A screen whose title is a whole block — an avatar, a name
  /// and a member count — passes false on both, because centring that between four controls leaves
  /// it nowhere to go.
  final bool? centerTitle;

  @override
  Widget build(BuildContext context) {
    final ios = isCupertino(context);
    final palette = GlassPalette.of(context);
    final centred = centerTitle ?? ios;

    final back = leading ?? _implicitLeading(context, ios);

    // Whether the clock and the battery are drawn light or dark.
    //
    // [AppBar] used to set this, and removing it took the setting with it — so the status bar kept
    // whatever the previous screen had asked for, which on a dark build meant black glyphs on a
    // black bar. Nothing is behind the status bar now but this bar's own glass, so the app's
    // brightness is the whole answer.
    final overlay = Theme.of(context).brightness == Brightness.dark
        ? SystemUiOverlayStyle.light
        : SystemUiOverlayStyle.dark;

    return AnnotatedRegion<SystemUiOverlayStyle>(
      value: overlay.copyWith(
        statusBarColor: Colors.transparent,
        systemNavigationBarColor: Colors.transparent,
      ),
      child: _bar(context, ios, palette, centred, back),
    );
  }

  Widget _bar(
    BuildContext context,
    bool ios,
    GlassPalette palette,
    bool centred,
    Widget? back,
  ) {
    return GlassSurface(
      borderRadius: BorderRadius.zero,
      // Only the bottom edge exists: the other three are off-screen, and drawing them puts a
      // hairline down the left and right of every screen.
      border: Border(bottom: BorderSide(color: palette.hairline, width: 0.5)),
      child: SafeArea(
        bottom: false,
        child: SizedBox(
          height: GlassMetrics.barHeight,
          child: Padding(
            // The gutter is halved against a control, because a 40pt circle inside a 44pt target
            // already carries 2pt of its own air on each side. Measuring to the target rather than
            // to the ink is what makes a bar look mis-padded.
            padding: EdgeInsets.symmetric(horizontal: GlassMetrics.gutter - 4),
            child: NavigationToolbar(
              centerMiddle: centred,
              middleSpacing: GlassMetrics.gap,
              leading: back,
              middle: title == null
                  ? null
                  : DefaultTextStyle.merge(
                      style: TextStyle(
                        fontSize: 17,
                        fontWeight: FontWeight.w600,
                        color: palette.foreground,
                      ),
                      overflow: TextOverflow.ellipsis,
                      child: title!,
                    ),
              trailing: actions.isEmpty
                  ? null
                  : Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        for (var i = 0; i < actions.length; i++) ...[
                          if (i > 0)
                            const SizedBox(width: GlassMetrics.gap - 4),
                          actions[i],
                        ],
                      ],
                    ),
            ),
          ),
        ),
      ),
    );
  }

  Widget? _implicitLeading(BuildContext context, bool ios) {
    if (!automaticallyImplyLeading) return null;
    if (!(ModalRoute.of(context)?.impliesAppBarDismissal ?? false)) return null;

    return GlassIconButton(
      icon: ios ? CupertinoIcons.back : Icons.arrow_back,
      semanticLabel: MaterialLocalizations.of(context).backButtonTooltip,
      onPressed: () => Navigator.maybePop(context),
    );
  }
}
