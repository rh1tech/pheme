// The message box, wherever the app has one.

import 'package:flutter/material.dart';

import 'glass_surface.dart';
import 'glass_tokens.dart';

/// The floating glass bar a message is written in.
///
/// A chat and a channel each had one of these, built separately: different paddings, different
/// border radii, a different attach glyph, and two different send buttons that had each been fixed
/// for the same unreadable-arrow bug in two different ways. They are the same control doing the
/// same job, so they are one widget now — the contents differ, the bar does not.
///
/// It floats clear of the bottom edge rather than being welded to it with a divider on top. That is
/// what lets the feed pass underneath, which is what tells the reader the conversation continues
/// past the composer; a divider only ever said "the screen ends here".
class GlassComposerBar extends StatelessWidget {
  const GlassComposerBar({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(
          GlassMetrics.gutter,
          0,
          GlassMetrics.gutter,
          GlassMetrics.gutter,
        ),
        child: GlassSurface(
          floating: true,
          borderRadius: BorderRadius.circular(26),
          padding: const EdgeInsets.all(6),
          child: child,
        ),
      ),
    );
  }
}

/// A borderless glyph inside a composer — attach a photo, cancel a reply, open the post options.
///
/// Deliberately not a [GlassIconButton]: those carry their own glass, and a glass circle inside a
/// glass bar is a surface on a surface. Same 44pt target, no material of its own.
class GlassComposerGlyph extends StatelessWidget {
  const GlassComposerGlyph({
    super.key,
    required this.icon,
    required this.semanticLabel,
    required this.onPressed,
    this.muted = false,
  });

  final IconData icon;
  final String semanticLabel;
  final VoidCallback? onPressed;

  /// Dims the glyph to say the option behind it is off, without disabling it.
  final bool muted;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);

    return IconButton(
      onPressed: onPressed,
      tooltip: semanticLabel,
      iconSize: 22,
      visualDensity: VisualDensity.compact,
      padding: const EdgeInsets.all(9),
      constraints: const BoxConstraints.tightFor(
        width: GlassMetrics.minTapTarget,
        height: GlassMetrics.minTapTarget,
      ),
      style: IconButton.styleFrom(
        foregroundColor: muted
            ? palette.mutedForeground.withValues(alpha: 0.5)
            : palette.mutedForeground,
        highlightColor: Colors.transparent,
      ),
      icon: Icon(icon),
    );
  }
}

/// Send: the one filled thing on the screen, so the eye finds it without looking.
class GlassSendButton extends StatelessWidget {
  const GlassSendButton({
    super.key,
    required this.sending,
    required this.enabled,
    required this.onPressed,
    required this.semanticLabel,
  });

  final bool sending;
  final bool enabled;
  final VoidCallback onPressed;
  final String semanticLabel;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Semantics(
      button: true,
      enabled: enabled,
      label: semanticLabel,
      child: GestureDetector(
        onTap: enabled ? onPressed : null,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 160),
          curve: Curves.easeOut,
          width: 38,
          height: 38,
          margin: const EdgeInsets.only(bottom: 1),
          decoration: BoxDecoration(
            // Fades to a flat disc rather than vanishing: a send button that disappears when the
            // box is empty leaves the row visibly lopsided while you type the first letter.
            color: enabled
                ? scheme.primary
                : scheme.onSurface.withValues(alpha: 0.10),
            shape: BoxShape.circle,
          ),
          child: sending
              ? Padding(
                  padding: const EdgeInsets.all(11),
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: scheme.onPrimary,
                  ),
                )
              : Icon(
                  Icons.arrow_upward_rounded,
                  size: 21,
                  // Pinned rather than inherited. Both composers had already been patched for this
                  // separately: the Material filled default resolved to a violet arrow on a violet
                  // disc, and the button was there but could not be read.
                  color: enabled
                      ? scheme.onPrimary
                      : scheme.onSurfaceVariant.withValues(alpha: 0.6),
                ),
        ),
      ),
    );
  }
}

/// The text field inside a composer: no fill and no border, because the glass IS the field's
/// surface and a filled box inside a translucent bar reads as a control inside a control.
class GlassComposerField extends StatelessWidget {
  const GlassComposerField({
    super.key,
    required this.controller,
    required this.hintText,
    this.focusNode,
    this.maxLines = 6,
    this.textCapitalization = TextCapitalization.sentences,
    this.onChanged,
  });

  final TextEditingController controller;
  final String hintText;
  final FocusNode? focusNode;
  final int maxLines;
  final TextCapitalization textCapitalization;
  final ValueChanged<String>? onChanged;

  @override
  Widget build(BuildContext context) {
    final palette = GlassPalette.of(context);

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 9),
      child: TextField(
        controller: controller,
        focusNode: focusNode,
        minLines: 1,
        maxLines: maxLines,
        textCapitalization: textCapitalization,
        onChanged: onChanged,
        textInputAction: TextInputAction.newline,
        keyboardType: TextInputType.multiline,
        style: const TextStyle(fontSize: 16, height: 1.3),
        decoration: InputDecoration(
          isDense: true,
          hintText: hintText,
          hintStyle: TextStyle(color: palette.mutedForeground),
          border: InputBorder.none,
          enabledBorder: InputBorder.none,
          focusedBorder: InputBorder.none,
          contentPadding: EdgeInsets.zero,
        ),
      ),
    );
  }
}
