import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';

/// A header that gets out of the way while the reader is scrolling, and comes back when they stop
/// heading away from it.
///
/// A search field is worth one row of the screen when you want it and nothing when you do not, and
/// on a phone that row is the difference between four messages visible and five. Every list in the
/// app kept one pinned regardless.
///
/// It hides on scrolling INTO the list and returns on scrolling back toward the top — not on any
/// movement, because a header that flickers on every small gesture is worse than one that never
/// moves at all.
class ScrollHidingHeader extends StatefulWidget {
  const ScrollHidingHeader({
    super.key,
    required this.header,
    required this.child,
    this.reversed = false,
  });

  /// The thing that hides. Usually a search field.
  final Widget header;

  /// The scrollable this listens to. Its notifications bubble up to here.
  final Widget child;

  /// Whether the scrollable runs backwards, as a message feed does.
  ///
  /// In a reversed list the offset grows as the reader moves toward OLDER content, so the gesture
  /// that means "further into the list" produces the opposite direction from a normal one. Without
  /// this the header would hide exactly when it should appear.
  final bool reversed;

  @override
  State<ScrollHidingHeader> createState() => _ScrollHidingHeaderState();
}

class _ScrollHidingHeaderState extends State<ScrollHidingHeader> {
  bool _visible = true;

  bool _onScroll(UserScrollNotification notification) {
    // Only the scrollable this wraps, not a horizontal strip of images inside a message.
    if (notification.metrics.axis != Axis.vertical) return false;

    final direction = notification.direction;
    if (direction == ScrollDirection.idle) return false;

    // `reverse` means the content is moving up the screen — the reader is going further into the
    // list. A reversed list inverts that.
    final goingIn = widget.reversed
        ? direction == ScrollDirection.forward
        : direction == ScrollDirection.reverse;

    // Nothing to hide from when there is nothing to scroll: a short list would otherwise lose its
    // search box to a stray gesture and have no way to bring it back.
    if (goingIn && notification.metrics.maxScrollExtent <= 0) return false;

    if (goingIn == _visible) setState(() => _visible = !goingIn);
    return false;
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        // Animated so the list does not jump by a row: the header shrinks into its own space and
        // the content follows it up.
        AnimatedSize(
          duration: const Duration(milliseconds: 180),
          curve: Curves.easeOut,
          alignment: Alignment.topCenter,
          child: _visible ? widget.header : const SizedBox.shrink(),
        ),
        Expanded(
          child: NotificationListener<UserScrollNotification>(
            onNotification: _onScroll,
            child: widget.child,
          ),
        ),
      ],
    );
  }
}
