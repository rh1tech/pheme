import 'package:flutter/material.dart';

import '../widgets/adaptive/adaptive.dart';
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
  messenger
    ..clearSnackBars()
    ..showSnackBar(
      SnackBar(
        content: Text(
          message,
          // Errors use the solid error color; force a high-contrast foreground
          // so the text is always legible (was white-on-light-red before).
          style: isError ? TextStyle(color: scheme.onError) : null,
        ),
        backgroundColor: isError ? scheme.error : null,
        closeIconColor: isError ? scheme.onError : null,
        showCloseIcon: true,
      ),
    );
}
