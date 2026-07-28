import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';

/// Reports its child's height after every layout.
///
/// Needed where a floating piece of chrome overlaps a scrollable and the scrollable has to reserve
/// exactly as much room as the chrome actually took: the composer grows with a reply quote, a strip
/// of attached photos and up to six lines of text, so no constant would be right for long. A guess
/// here shows up as the newest message sitting behind the send button.
///
/// The callback fires from a post-frame callback rather than during layout, because the listener's
/// job is to rebuild and rebuilding mid-layout is not allowed.
class MeasuredHeight extends SingleChildRenderObjectWidget {
  const MeasuredHeight({
    super.key,
    required this.onChange,
    required Widget super.child,
  });

  final ValueChanged<double> onChange;

  @override
  RenderObject createRenderObject(BuildContext context) =>
      _RenderMeasuredHeight(onChange);

  @override
  void updateRenderObject(
    BuildContext context,
    covariant RenderProxyBox renderObject,
  ) {
    (renderObject as _RenderMeasuredHeight).onChange = onChange;
  }
}

class _RenderMeasuredHeight extends RenderProxyBox {
  _RenderMeasuredHeight(this.onChange);

  ValueChanged<double> onChange;
  double _reported = -1;

  @override
  void performLayout() {
    super.performLayout();
    final height = size.height;
    if (height == _reported) return;
    _reported = height;
    WidgetsBinding.instance.addPostFrameCallback((_) => onChange(height));
  }
}
