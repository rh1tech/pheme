// The fallback avatar, drawn rather than fetched.
//
// A notification for someone with no profile picture used to show EMPTY SPACE where the avatar
// goes — not the app icon, not initials, just a gap — because the avatar path only ever fetched a
// URL and there is no URL when there is no picture. Everywhere else in the app that person appears
// as a coloured circle with their initials, so a notification was the one place they became
// anonymous.
//
// The colour and the letters come from the same helpers the chat UI uses (conversation_avatar.dart),
// so the circle in a notification is the same circle as the one in the conversation list. That is
// the whole point: if these drifted apart, the same person would be two different colours depending
// on where you looked at them.

import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/painting.dart';

import '../chat/widgets/conversation_avatar.dart';

/// A round avatar with [label]'s initials on a colour hashed from [id], as PNG bytes.
///
/// Returns null on any failure. Callers draw the notification without a picture in that case, which
/// is exactly what they did before this existed — a missing avatar must never cost the message.
///
/// [size] is in physical pixels. Notification icons are small and shown at a fixed size; 128 is
/// comfortably above what any launcher asks for and still trivial to produce.
Future<Uint8List?> initialsAvatarPng({
  required String id,
  required String label,
  int size = 128,
}) async {
  try {
    final recorder = ui.PictureRecorder();
    final canvas = ui.Canvas(recorder);
    final extent = size.toDouble();
    final radius = extent / 2;

    canvas.drawCircle(
      ui.Offset(radius, radius),
      radius,
      ui.Paint()..color = avatarColor(id),
    );

    // Proportional to the circle, so the letters sit the same way at any size. 0.4 is what the
    // widget uses for its own text, and matching it keeps the two visually identical.
    final painter = TextPainter(
      text: TextSpan(
        text: avatarInitials(label),
        style: TextStyle(
          color: const ui.Color(0xFFFFFFFF),
          fontSize: extent * 0.4,
          fontWeight: FontWeight.w600,
          // A notification is drawn by the system, and the system's idea of the default font is not
          // necessarily the app's. Naming nothing here takes whatever the platform offers, which is
          // what the rest of this notification is set in anyway.
        ),
      ),
      textDirection: TextDirection.ltr,
      textAlign: TextAlign.center,
    )..layout();

    painter.paint(
      canvas,
      ui.Offset(
        radius - painter.width / 2,
        // Optical centring: TextPainter's height includes the font's ascent and descent, so
        // centring on it is the correct vertical middle of the line box.
        radius - painter.height / 2,
      ),
    );

    final image = await recorder.endRecording().toImage(size, size);
    final data = await image.toByteData(format: ui.ImageByteFormat.png);
    image.dispose();
    return data?.buffer.asUint8List();
  } on Object {
    // Rasterising can fail in a background isolate on some engines, and a notification without a
    // picture is a working notification.
    return null;
  }
}
