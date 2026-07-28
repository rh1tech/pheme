import 'package:flutter/material.dart';

import '../widgets/adaptive/adaptive.dart';
import '../widgets/glass/glass.dart';
import 'api_exception.dart';

/// Shows a transient success message. Centralises feedback styling so screens
/// never call ScaffoldMessenger directly (mirrors the web app's lib/notify).
void notifySuccess(BuildContext context, String message) {
  _show(context, message, isError: false);
}

/// Shows an error message. If [error] is provided and carries a server message,
/// it is appended for context.
void notifyError(BuildContext context, String message, [Object? error]) {
  final detail = _detail(error);
  _show(
    context,
    detail == null || detail == message ? message : '$message: $detail',
    isError: true,
  );
}

String? _detail(Object? error) {
  if (error == null) return null;
  if (error is ApiException) return error.message;
  if (error is AuthException) return error.message;
  return error.toString();
}

void _show(BuildContext context, String message, {required bool isError}) {
  // iOS has no snackbar idiom — use a transient overlay toast instead.
  if (isCupertino(context)) {
    showCupertinoToast(context, message, isError: isError);
    return;
  }
  final messenger = ScaffoldMessenger.maybeOf(context);
  if (messenger == null) return;
  final scheme = Theme.of(context).colorScheme;

  // Above the floating tab bar, not behind it.
  //
  // The theme already floats these with a 12pt inset all round, which puts them exactly where the
  // tab bar is — so on Chats and Channels a message was reported underneath the chrome that was
  // painted over it. Same root cause as the action button: the bar floats INSIDE the scaffold whose
  // messenger this is, so nothing about the scaffold's own geometry knows it is there.
  //
  // Zero on a pushed page, which has no tab bar and wants the plain inset.
  final chrome = BottomChrome.of(context);
  messenger
    ..clearSnackBars()
    ..showSnackBar(
      SnackBar(
        content: Text(
          message,
          // Regular weight on a near-black slab, matching the iOS toast exactly. The Material
          // default is the theme's inverse surface, which under this app's light theme is a mid
          // grey carrying semi-bold white — legible, but louder than the thing it is reporting.
          style: const TextStyle(
            color: Colors.white,
            fontWeight: FontWeight.w400,
          ),
        ),
        backgroundColor: isError ? scheme.error : const Color(0xFF1C1C1E),
        closeIconColor: Colors.white,
        showCloseIcon: true,
        // Stated here rather than inherited. A margin is only legal on a FLOATING snackbar —
        // Flutter asserts otherwise — and leaving the behaviour to the theme means a surface built
        // without it crashes instead of merely looking wrong. The two have to travel together.
        behavior: SnackBarBehavior.floating,
        margin: EdgeInsets.fromLTRB(
          GlassMetrics.gutter,
          0,
          GlassMetrics.gutter,
          chrome + GlassMetrics.gutter,
        ),
      ),
    );
}
