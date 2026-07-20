import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

/// Conversation shortcuts, which are what let Android draw a message notification as a
/// CONVERSATION: the sender's avatar as the notification's own icon, with the app icon badged into
/// its corner.
///
/// MessagingStyle on its own does not get you that. Without a long-lived dynamic shortcut whose id
/// the notification quotes, the system renders it in the ordinary section — app icon in the main
/// slot with "Pheme" written above it, and the avatar demoted to a small circle beside the message.
///
/// The shortcut has to exist BEFORE the notification arrives, because publishing it needs the
/// Activity's method channel and a push handled by the background isolate cannot reach one. So it is
/// published when a conversation is seen while the app is running, and Android keeps it afterwards.
class ConversationShortcuts {
  static const _channel = MethodChannel('pheme/conversations');

  /// Publishes (or refreshes) the shortcut for one conversation.
  ///
  /// Best effort by design: a shortcut that fails to publish costs the notification its
  /// conversation treatment and nothing else. It must never cost the notification itself, and it
  /// must never fail a screen that was only trying to list some chats.
  static Future<void> publish({
    required String conversationId,
    required String name,
    Uint8List? avatar,
  }) async {
    if (conversationId.isEmpty) return;
    try {
      await _channel.invokeMethod<void>('publishShortcut', {
        'id': conversationId,
        'name': name,
        'avatar': avatar,
      });
    } on PlatformException catch (e) {
      debugPrint('Pheme: conversation shortcut not published: ${e.message}');
    } on MissingPluginException {
      // Every platform but Android, and the background isolate. Neither is an error: the
      // notification simply renders without conversation treatment.
    }
  }
}
