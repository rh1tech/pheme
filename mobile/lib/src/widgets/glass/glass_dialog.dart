// Alerts and confirmations.

import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'glass_surface.dart';
import 'glass_tokens.dart';

/// A dialog in the app's own material.
///
/// Deliberately API-compatible with [AlertDialog] — `title`, `content`, `actions` — because every
/// dialog in the app was one, and a shape that matches converts them by changing the constructor
/// name rather than by rewriting the call site.
///
/// The app used to show three different dialogs depending on where you were standing: a Material
/// [AlertDialog] on Android, a [CupertinoAlertDialog] on iOS, and a raw [AlertDialog] on both from
/// the half-dozen places that called [showDialog] directly and never got the adaptive treatment. The
/// chrome around them is one design now, so these are too.
class GlassDialog extends StatelessWidget {
  const GlassDialog({
    super.key,
    this.title,
    this.content,
    this.actions = const [],
    this.stackActions = false,
  });

  final Widget? title;
  final Widget? content;
  final List<Widget> actions;

  /// Forces two actions onto separate rows, overriding [_shouldStack].
  final bool stackActions;

  /// Whether the actions have to stack.
  ///
  /// Two short words sit side by side happily; "Start fresh on this device" does not, and half a
  /// dialog's width is all a side-by-side pair ever gets — so it wrapped to two lines against a
  /// one-line neighbour, and the pair looked broken rather than merely tight.
  ///
  /// Measured from the labels rather than declared by the caller. [GlassDialogAction] carries its
  /// own label, so the dialog can look; asking every call site to flag its own long strings is a
  /// rule that holds until the first translation makes a short label long.
  bool get _shouldStack {
    if (stackActions) return true;
    if (actions.length != 2) return actions.length > 2;

    final labelled = actions.whereType<GlassDialogAction>().toList();
    // An action that is not a GlassDialogAction cannot be measured; side by side is the safe
    // assumption, since that is what a pair of plain buttons expects.
    if (labelled.length != 2) return false;
    // The LONGER label decides, not the pair's total: each one gets half the width whatever its
    // neighbour does, so a short "Restore" buys "Start fresh on this device" nothing.
    return labelled.map((a) => a.label.length).reduce(math.max) > _halfWidthMax;
  }

  /// Roughly what one label fits across half a dialog at 16pt before it wraps. Deliberately
  /// conservative: a stacked pair that did not need to stack looks fine, and a wrapped label
  /// beside an unwrapped one looks like a bug.
  static const int _halfWidthMax = 14;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);

    // Centred in what is LEFT of the screen, not in the screen.
    //
    // A dialog with a field in it raises the keyboard, and a plain Center goes on centring against
    // the full height — so the bottom of the dialog, which is where its buttons are, ends up behind
    // the keyboard. Taking the inset as padding shrinks the box the dialog centres within, and it
    // rises to meet the keyboard instead.
    //
    // Animated, because the keyboard is: matching its rise stops the dialog from jumping into place
    // after it. And scrollable, because a tall dialog on a short remaining strip has to give
    // somewhere, and scrolling is better than clipping the buttons off.
    return AnimatedPadding(
      duration: const Duration(milliseconds: 180),
      curve: Curves.easeOut,
      padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
      child: Center(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 400),
            child: SingleChildScrollView(
              child: GlassSurface(
                floating: true,
                borderRadius: BorderRadius.circular(24),
                // Same reason the menu carries one: a dialog route has no Material above it, so raw
                // Text would fall back to the debug error style — the yellow double underline.
                child: Material(
                  type: MaterialType.transparency,
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Padding(
                        padding: const EdgeInsets.fromLTRB(24, 22, 24, 0),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            if (title != null)
                              DefaultTextStyle.merge(
                                style: TextStyle(
                                  fontSize: 18,
                                  fontWeight: FontWeight.w600,
                                  color: palette.foreground,
                                ),
                                child: title!,
                              ),
                            if (title != null && content != null)
                              const SizedBox(height: 10),
                            if (content != null)
                              DefaultTextStyle.merge(
                                style: TextStyle(
                                  fontSize: 15,
                                  height: 1.35,
                                  color: palette.mutedForeground,
                                ),
                                child: content!,
                              ),
                          ],
                        ),
                      ),
                      if (actions.isNotEmpty) ...[
                        const SizedBox(height: 20),
                        Divider(
                          height: 0.5,
                          thickness: 0.5,
                          color: palette.hairline,
                        ),
                        if (!_shouldStack && actions.length == 2)
                          IntrinsicHeight(
                            child: Row(
                              children: [
                                Expanded(child: actions[0]),
                                VerticalDivider(
                                  width: 0.5,
                                  thickness: 0.5,
                                  color: palette.hairline,
                                ),
                                Expanded(child: actions[1]),
                              ],
                            ),
                          )
                        else
                          for (var i = 0; i < actions.length; i++) ...[
                            if (i > 0)
                              Divider(
                                height: 0.5,
                                thickness: 0.5,
                                color: palette.hairline,
                              ),
                            actions[i],
                          ],
                      ] else
                        const SizedBox(height: 22),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A button along the foot of a [GlassDialog].
class GlassDialogAction extends StatefulWidget {
  const GlassDialogAction({
    super.key,
    required this.label,
    required this.onPressed,
    this.destructive = false,
    this.emphasised = false,
  });

  final String label;
  final VoidCallback? onPressed;

  /// The one that ends something. Drawn in the error colour.
  final bool destructive;

  /// The action the dialog is asking for, as opposed to the way out of it.
  final bool emphasised;

  @override
  State<GlassDialogAction> createState() => _GlassDialogActionState();
}

class _GlassDialogActionState extends State<GlassDialogAction> {
  bool _down = false;

  void _set(bool down) {
    if (_down != down) setState(() => _down = down);
  }

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);
    final scheme = Theme.of(context).colorScheme;
    final enabled = widget.onPressed != null;

    final color = widget.destructive
        ? scheme.error
        : widget.emphasised
        ? palette.accent
        : palette.foreground;

    return Semantics(
      button: true,
      enabled: enabled,
      label: widget.label,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTapDown: enabled ? (_) => _set(true) : null,
        onTapUp: enabled ? (_) => _set(false) : null,
        onTapCancel: enabled ? () => _set(false) : null,
        onTap: widget.onPressed,
        child: ExcludeSemantics(
          child: AnimatedContainer(
            duration: GlassMetrics.pressDuration,
            color: _down
                ? palette.foreground.withValues(alpha: 0.07)
                : Colors.transparent,
            padding: const EdgeInsets.symmetric(vertical: 15, horizontal: 12),
            child: Text(
              widget.label,
              textAlign: TextAlign.center,
              style: TextStyle(
                fontSize: 16,
                fontWeight: widget.emphasised
                    ? FontWeight.w600
                    : FontWeight.w500,
                color: enabled ? color : color.withValues(alpha: 0.4),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Shows a [GlassDialog], with the app's barrier rather than [showDialog]'s flat black.
Future<T?> showGlassDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  bool barrierDismissible = true,
}) {
  return showDialog<T>(
    context: context,
    barrierDismissible: barrierDismissible,
    barrierColor: Colors.black.withValues(alpha: 0.32),
    builder: builder,
  );
}

/// The common case: a question with a way out of it. Resolves to `true` when confirmed.
Future<bool> showGlassConfirm(
  BuildContext context, {
  required String title,
  String? message,
  required String confirmLabel,
  required String cancelLabel,
  bool isDestructive = false,
}) async {
  final result = await showGlassDialog<bool>(
    context: context,
    builder: (ctx) => GlassDialog(
      title: Text(title),
      content: message == null ? null : Text(message),
      actions: [
        GlassDialogAction(
          label: cancelLabel,
          onPressed: () => Navigator.of(ctx).pop(false),
        ),
        GlassDialogAction(
          label: confirmLabel,
          destructive: isDestructive,
          emphasised: !isDestructive,
          onPressed: () => Navigator.of(ctx).pop(true),
        ),
      ],
    ),
  );
  return result ?? false;
}
