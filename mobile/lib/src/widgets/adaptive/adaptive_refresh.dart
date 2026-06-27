import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import 'platform.dart';

/// A pull-to-refresh scroll view that uses the native affordance per platform:
/// [CupertinoSliverRefreshControl] on iOS and Material [RefreshIndicator] on
/// Android. Callers supply [slivers] (e.g. [SliverList], [SliverPadding]).
class AdaptiveRefreshableScrollView extends StatelessWidget {
  const AdaptiveRefreshableScrollView({
    super.key,
    required this.onRefresh,
    required this.slivers,
  });

  final Future<void> Function() onRefresh;
  final List<Widget> slivers;

  @override
  Widget build(BuildContext context) {
    const physics = AlwaysScrollableScrollPhysics();
    if (isCupertino(context)) {
      return CustomScrollView(
        physics: physics,
        slivers: [
          CupertinoSliverRefreshControl(onRefresh: onRefresh),
          ...slivers,
        ],
      );
    }
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: CustomScrollView(physics: physics, slivers: slivers),
    );
  }
}
