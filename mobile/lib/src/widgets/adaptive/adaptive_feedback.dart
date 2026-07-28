import 'dart:math' as math;

import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';

import '../glass/glass_dialog.dart';

/// Shows a confirmation dialog. Resolves to `true` when confirmed.
///
/// One dialog on both platforms now — see [GlassDialog]. This used to fork into a
/// [CupertinoAlertDialog] and a Material [AlertDialog], which was the right instinct while the rest
/// of the app was also forking; it is the wrong one now that every bar, menu and sheet around it is
/// the same design on both.
///
/// Kept as a named function rather than folded into its one-line body, because a dozen call sites
/// ask this question and they should keep asking it in one voice.
Future<bool> showAdaptiveConfirm(
  BuildContext context, {
  required String title,
  String? message,
  required String confirmLabel,
  required String cancelLabel,
  bool isDestructive = false,
}) {
  return showGlassConfirm(
    context,
    title: title,
    message: message,
    confirmLabel: confirmLabel,
    cancelLabel: cancelLabel,
    isDestructive: isDestructive,
  );
}

/// Transient overlay toast used as the iOS replacement for a Material SnackBar.
/// Android keeps the real SnackBar (see `core/snackbar.dart`).
void showCupertinoToast(
  BuildContext context,
  String message, {
  bool isError = false,
}) {
  final overlay = Overlay.maybeOf(context, rootOverlay: true);
  if (overlay == null) return;
  late final OverlayEntry entry;
  entry = OverlayEntry(
    builder: (_) =>
        _Toast(message: message, isError: isError, onDismissed: entry.remove),
  );
  overlay.insert(entry);
}

class _Toast extends StatefulWidget {
  const _Toast({
    required this.message,
    required this.isError,
    required this.onDismissed,
  });

  final String message;
  final bool isError;
  final VoidCallback onDismissed;

  @override
  State<_Toast> createState() => _ToastState();
}

class _ToastState extends State<_Toast> with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 220),
  );

  @override
  void initState() {
    super.initState();
    _run();
  }

  Future<void> _run() async {
    await _controller.forward();
    await Future<void>.delayed(const Duration(milliseconds: 2600));
    if (!mounted) return;
    await _controller.reverse();
    widget.onDismissed();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final media = MediaQuery.of(context);
    // Near-black rather than systemGrey. A grey pill on a light page reads as a disabled control
    // and on a dark one it disappears into the background; the classic toast is a dark slab that
    // belongs to neither theme and is legible over both.
    final bg = widget.isError
        ? CupertinoColors.systemRed.resolveFrom(context)
        : const Color(0xFF1C1C1E);
    // Above the keyboard when there is one.
    //
    // The toast is inserted into the ROOT overlay, which is not resized for the keyboard the way a
    // scaffold's body is — so a message reported while typing was drawn underneath it, in the one
    // situation where the reason for the message is usually what was just typed. viewInsets is the
    // keyboard; padding.bottom is the home indicator, and a platform reports it as zero while the
    // keyboard is up, so taking the larger of the two is right in both states rather than additive.
    final lift = math.max(media.viewInsets.bottom, media.padding.bottom);
    return Positioned(
      left: 24,
      right: 24,
      bottom: lift + 24,
      child: SafeArea(
        child: FadeTransition(
          opacity: _controller,
          child: Center(
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              decoration: BoxDecoration(
                color: bg.withValues(alpha: 0.94),
                borderRadius: BorderRadius.circular(14),
              ),
              child: Text(
                widget.message,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: CupertinoColors.white,
                  fontSize: 14,
                  // Stated rather than inherited: a toast is drawn straight into the overlay, with
                  // no DefaultTextStyle above it, so an unset weight is whatever the ambient style
                  // happens to be — which is how this ended up bold.
                  fontWeight: FontWeight.w400,
                  decoration: TextDecoration.none,
                  fontFamily: '.SF Pro Text',
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
