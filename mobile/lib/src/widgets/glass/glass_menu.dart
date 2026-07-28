// The overflow menu.

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../adaptive/platform.dart';
import 'glass_surface.dart';
import 'glass_tokens.dart';

/// One line of a [GlassMenuButton]'s menu.
@immutable
class GlassMenuAction {
  const GlassMenuAction({
    required this.label,
    required this.onSelected,
    this.icon,
    this.destructive = false,
  });

  final String label;
  final VoidCallback onSelected;
  final IconData? icon;

  /// Draws the row in the error colour. For the ones that end something — delete a chat, leave a
  /// group — which a reader should be able to pick out without reading the whole menu.
  final bool destructive;
}

/// Opens a glass menu under [context]'s widget.
///
/// [context] must be the TRIGGER's own context — the thing the menu should hang off — because that
/// is what the anchor rectangle is measured from. Wrap the trigger in a [Builder] if the nearest
/// context to hand belongs to something bigger.
///
/// [PopupMenuButton] was doing this job, and it could be given a colour and a corner radius but not
/// a material: it is an opaque Material card with Material's own row height, its own text style and
/// its own ink splash, which is precisely the stray platform default the rest of the chrome was
/// rewritten to get away from. Next to a glass bar it reads as a menu borrowed from another app.
///
/// This is the same glass as the bar it drops out of, anchored under whatever opened it and growing
/// from that corner. It is a route rather than a bare [OverlayEntry], so the back gesture, the
/// barrier and the dismissal come from the navigator instead of being hand-rolled.
Future<void> showGlassMenu(
  BuildContext context,
  List<GlassMenuAction> actions,
) async {
  final trigger = context.findRenderObject();
  final overlay = Navigator.of(context).overlay?.context.findRenderObject();
  if (trigger is! RenderBox || overlay is! RenderBox) return;

  final anchor = Rect.fromPoints(
    trigger.localToGlobal(Offset.zero, ancestor: overlay),
    trigger.localToGlobal(
      trigger.size.bottomRight(Offset.zero),
      ancestor: overlay,
    ),
  );

  final chosen = await Navigator.of(context).push<GlassMenuAction>(
    _GlassMenuRoute(
      actions: actions,
      anchor: anchor,
      overlaySize: overlay.size,
    ),
  );
  chosen?.onSelected();
}

/// Any widget turned into a menu trigger. The [Builder] that [showGlassMenu] needs, supplied once
/// here rather than at every call site.
class GlassMenuAnchor extends StatelessWidget {
  const GlassMenuAnchor({
    super.key,
    required this.child,
    required this.actions,
    required this.semanticLabel,
    this.enabled = true,
  });

  final Widget child;
  final List<GlassMenuAction> actions;
  final String semanticLabel;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      button: true,
      enabled: enabled,
      label: semanticLabel,
      child: Builder(
        builder: (context) => GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: enabled ? () => showGlassMenu(context, actions) : null,
          child: ExcludeSemantics(child: child),
        ),
      ),
    );
  }
}

/// The bar's overflow control: a glass circle that opens a glass menu.
class GlassMenuButton extends StatelessWidget {
  const GlassMenuButton({
    super.key,
    required this.actions,
    required this.semanticLabel,
    this.icon,
  });

  final List<GlassMenuAction> actions;
  final String semanticLabel;

  /// Defaults to the platform's overflow glyph.
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final glyph =
        icon ??
        (isCupertino(context) ? CupertinoIcons.ellipsis : Icons.more_vert);

    return GlassMenuAnchor(
      actions: actions,
      semanticLabel: semanticLabel,
      child: SizedBox.square(
        dimension: GlassMetrics.minTapTarget,
        child: Center(
          child: SizedBox.square(
            dimension: GlassMetrics.control,
            child: GlassSurface(
              borderRadius: BorderRadius.circular(GlassMetrics.control / 2),
              child: Icon(
                glyph,
                size: GlassMetrics.icon,
                color: palette.foreground,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _GlassMenuRoute extends PopupRoute<GlassMenuAction> {
  _GlassMenuRoute({
    required this.actions,
    required this.anchor,
    required this.overlaySize,
  });

  final List<GlassMenuAction> actions;
  final Rect anchor;
  final Size overlaySize;

  static const double _width = 232;
  static const double _gap = 6;

  @override
  Duration get transitionDuration => const Duration(milliseconds: 150);

  @override
  Duration get reverseTransitionDuration => const Duration(milliseconds: 110);

  @override
  bool get barrierDismissible => true;

  @override
  String? get barrierLabel => 'Dismiss menu';

  // No scrim. The menu is small and the screen behind it stays legible; dimming the whole app for a
  // two-item list is a heavier gesture than the list deserves.
  @override
  Color? get barrierColor => null;

  @override
  Widget buildPage(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
  ) {
    // Right edge under the button's right edge, so the menu reads as belonging to it — clamped so a
    // control near the screen edge does not push it off.
    final right = (overlaySize.width - anchor.right).clamp(
      GlassMetrics.gutter,
      overlaySize.width - _width - GlassMetrics.gutter,
    );

    return Stack(
      children: [
        Positioned(
          top: anchor.bottom + _gap,
          right: right.toDouble(),
          width: _width,
          child: _GlassMenu(actions: actions),
        ),
      ],
    );
  }

  @override
  Widget buildTransitions(
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) {
    final curved = CurvedAnimation(
      parent: animation,
      curve: Curves.easeOutCubic,
      reverseCurve: Curves.easeIn,
    );
    // Grows out of the button's corner rather than appearing whole.
    return FadeTransition(
      opacity: curved,
      child: ScaleTransition(
        scale: Tween<double>(begin: 0.88, end: 1).animate(curved),
        alignment: Alignment.topRight,
        child: child,
      ),
    );
  }
}

class _GlassMenu extends StatelessWidget {
  const _GlassMenu({required this.actions});

  final List<GlassMenuAction> actions;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);

    return GlassSurface(
      floating: true,
      borderRadius: BorderRadius.circular(18),
      // A PopupRoute's content hangs off the navigator's overlay with NOTHING above it — no
      // Scaffold, no Material, and so no DefaultTextStyle. Text in that position falls back to the
      // debug error style, which is what the yellow double underline under every menu item was: not
      // a decoration anyone chose, but Flutter saying "this text has no style to inherit". An
      // explicit style on the Text does not help, because a Text MERGES onto the inherited style
      // and inherits the decoration it never set.
      //
      // MaterialType.transparency paints nothing, so the glass shows through unchanged.
      child: Material(
        type: MaterialType.transparency,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (var i = 0; i < actions.length; i++) ...[
              if (i > 0)
                Divider(height: 0.5, thickness: 0.5, color: palette.hairline),
              _GlassMenuItem(action: actions[i]),
            ],
          ],
        ),
      ),
    );
  }
}

class _GlassMenuItem extends StatefulWidget {
  const _GlassMenuItem({required this.action});

  final GlassMenuAction action;

  @override
  State<_GlassMenuItem> createState() => _GlassMenuItemState();
}

class _GlassMenuItemState extends State<_GlassMenuItem> {
  bool _down = false;

  void _set(bool down) {
    if (_down != down) setState(() => _down = down);
  }

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final scheme = Theme.of(context).colorScheme;
    final color = widget.action.destructive ? scheme.error : palette.foreground;

    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTapDown: (_) => _set(true),
      onTapUp: (_) => _set(false),
      onTapCancel: () => _set(false),
      // Pop first, act second: several of these open a dialog of their own, and pushing one while
      // the menu is still coming down leaves the two animations fighting.
      onTap: () => Navigator.of(context).pop(widget.action),
      child: AnimatedContainer(
        duration: GlassMetrics.pressDuration,
        color: _down
            ? palette.foreground.withValues(alpha: 0.07)
            : Colors.transparent,
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 13),
        child: Row(
          children: [
            Expanded(
              child: Text(
                widget.action.label,
                style: TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w500,
                  color: color,
                ),
              ),
            ),
            if (widget.action.icon != null) ...[
              const SizedBox(width: 12),
              Icon(widget.action.icon, size: 19, color: color),
            ],
          ],
        ),
      ),
    );
  }
}
