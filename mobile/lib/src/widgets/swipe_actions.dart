// Swipe a row aside to reveal what you can do to it.

import 'package:flutter/material.dart';

import 'glass/glass_tokens.dart';

/// One button behind a [SwipeActions] row.
@immutable
class SwipeAction {
  const SwipeAction({
    required this.label,
    required this.icon,
    required this.onPressed,
    required this.color,
  });

  final String label;
  final IconData icon;
  final VoidCallback onPressed;
  final Color color;
}

/// A row that slides left to uncover its actions, and closes again when you let go short of them,
/// tap the row, or open a different one.
///
/// Not `Dismissible`: that is swipe-to-DELETE, where the gesture itself is the decision and the row
/// is already gone by the time you see what you did. This is the affordance both platforms actually
/// use in a list of conversations — the swipe reveals a button, and the button is the decision. It
/// also means the destructive action can carry a word rather than being a direction you have to
/// know about.
///
/// Hand-rolled rather than taken from a package: it is one gesture and one AnimatedBuilder, and the
/// buttons have to be cut from this app's design language anyway.
class SwipeActions extends StatefulWidget {
  const SwipeActions({
    super.key,
    required this.child,
    required this.actions,
    this.controller,
  });

  final Widget child;
  final List<SwipeAction> actions;

  /// Shared between the rows of one list so that opening a row closes its neighbours. Optional: a
  /// lone row works without one.
  final SwipeActionsController? controller;

  /// Width of one action button.
  static const double actionWidth = 84;

  @override
  State<SwipeActions> createState() => _SwipeActionsState();
}

/// Remembers which row is open, so a list never shows two sets of buttons at once.
class SwipeActionsController extends ChangeNotifier {
  Object? _open;

  Object? get openRow => _open;

  void opened(Object row) {
    if (_open == row) return;
    _open = row;
    notifyListeners();
  }

  void closed(Object row) {
    if (_open != row) return;
    _open = null;
    notifyListeners();
  }

  void closeAll() {
    if (_open == null) return;
    _open = null;
    notifyListeners();
  }
}

class _SwipeActionsState extends State<SwipeActions>
    with SingleTickerProviderStateMixin {
  late final AnimationController _slide = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 220),
  );

  double get _extent => widget.actions.length * SwipeActions.actionWidth;

  @override
  void initState() {
    super.initState();
    widget.controller?.addListener(_onControllerChanged);
  }

  @override
  void dispose() {
    widget.controller?.removeListener(_onControllerChanged);
    _slide.dispose();
    super.dispose();
  }

  /// Another row opened: this one gets out of the way.
  void _onControllerChanged() {
    if (widget.controller?.openRow != this && _slide.value != 0) {
      _slide.animateTo(0, curve: Curves.easeOut);
    }
  }

  void _open() {
    widget.controller?.opened(this);
    _slide.animateTo(1, curve: Curves.easeOut);
  }

  void _close() {
    widget.controller?.closed(this);
    _slide.animateTo(0, curve: Curves.easeOut);
  }

  void _onDragUpdate(DragUpdateDetails details) {
    // Negative dx is a leftward drag. Clamped at both ends: dragging right from closed must not
    // peel the row off its leading edge, and dragging past the buttons must not open a gap.
    final next = _slide.value - details.primaryDelta! / _extent;
    _slide.value = next.clamp(0.0, 1.0);
  }

  void _onDragEnd(DragEndDetails details) {
    // A flick decides on its own; a slow drag is judged on how far it got. Without the velocity
    // case a quick, short swipe — which is what the gesture actually looks like in use — would
    // spring back and feel like it had been refused.
    const flick = 400.0;
    final velocity = details.primaryVelocity ?? 0;
    if (velocity < -flick) return _open();
    if (velocity > flick) return _close();
    _slide.value > 0.4 ? _open() : _close();
  }

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onHorizontalDragUpdate: _onDragUpdate,
      onHorizontalDragEnd: _onDragEnd,
      child: AnimatedBuilder(
        animation: _slide,
        builder: (context, child) {
          final offset = _slide.value * _extent;
          return Stack(
            children: [
              // The buttons live behind the row and are only hit-testable once there is something
              // to hit: at rest they are a zero-width strip, so a tap near the edge of a closed row
              // opens the chat rather than deleting it.
              Positioned.fill(
                child: Align(
                  alignment: Alignment.centerRight,
                  child: ClipRect(
                    child: SizedBox(
                      width: offset,
                      child: OverflowBox(
                        alignment: Alignment.centerLeft,
                        maxWidth: _extent,
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            for (final action in widget.actions)
                              _ActionButton(
                                action: action,
                                onPressed: () {
                                  _close();
                                  action.onPressed();
                                },
                              ),
                          ],
                        ),
                      ),
                    ),
                  ),
                ),
              ),
              Transform.translate(
                offset: Offset(-offset, 0),
                child: _slide.value == 0
                    ? child
                    // While open, a tap anywhere on the row closes it instead of opening the chat.
                    // Opening what you were about to delete is a nasty way to find out the row had
                    // moved.
                    : GestureDetector(
                        behavior: HitTestBehavior.opaque,
                        onTap: _close,
                        child: IgnorePointer(child: child),
                      ),
              ),
            ],
          );
        },
        child: widget.child,
      ),
    );
  }
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({required this.action, required this.onPressed});

  final SwipeAction action;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      label: action.label,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onPressed,
        child: Container(
          width: SwipeActions.actionWidth,
          height: double.infinity,
          color: action.color,
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(action.icon, color: Colors.white, size: GlassMetrics.icon),
              const SizedBox(height: 4),
              Text(
                action.label,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
