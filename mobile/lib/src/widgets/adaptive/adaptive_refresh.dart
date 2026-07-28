import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../glass/glass_tokens.dart';
import 'platform.dart';

/// A pull-to-refresh scroll view that uses the native affordance per platform:
/// [CupertinoSliverRefreshControl] on iOS and Material [RefreshIndicator] on
/// Android. Callers supply [slivers] (e.g. [SliverList], [SliverPadding]).
///
/// It also spends the page's [MediaQuery] padding — which under [AdaptiveScaffold] includes the
/// height of the glass bar and, on a tab, the floating tab bar — as content padding rather than as
/// viewport inset. That is the difference between a list that scrolls *under* the chrome and one
/// that starts below it, and it is why callers do not have to think about the bar at all: the first
/// row is reachable, the last row clears the tab bar, and the space between them is glass.
class AdaptiveRefreshableScrollView extends StatelessWidget {
  const AdaptiveRefreshableScrollView({
    super.key,
    required this.onRefresh,
    required this.slivers,
  });

  final Future<void> Function() onRefresh;
  final List<Widget> slivers;

  /// Dragging the list puts the keyboard away.
  ///
  /// The other half of getting out of a search — see the Cancel button on [AdaptiveSearchField].
  /// Reaching for the list is what a reader does when they have finished typing and want to look at
  /// the results, so it is the gesture that should mean "done".
  static const _dismiss = ScrollViewKeyboardDismissBehavior.onDrag;

  @override
  Widget build(BuildContext context) {
    const physics = AlwaysScrollableScrollPhysics();
    final padding = MediaQuery.paddingOf(context);

    final top = SliverToBoxAdapter(child: SizedBox(height: padding.top));
    final bottom = SliverToBoxAdapter(
      child: SizedBox(height: padding.bottom + GlassMetrics.gutter),
    );

    if (isCupertino(context)) {
      return CustomScrollView(
        physics: physics,
        keyboardDismissBehavior: _dismiss,
        slivers: [
          // After the top spacer, so the spinner unrolls below the bar rather than behind it.
          top,
          CupertinoSliverRefreshControl(onRefresh: onRefresh),
          ...slivers,
          bottom,
        ],
      );
    }
    return RefreshIndicator(
      onRefresh: onRefresh,
      // Same reason: the Material indicator drops from the top of the VIEWPORT, which is now behind
      // the glass.
      edgeOffset: padding.top,
      child: CustomScrollView(
        physics: physics,
        keyboardDismissBehavior: _dismiss,
        slivers: [top, ...slivers, bottom],
      ),
    );
  }
}
