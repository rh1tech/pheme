import 'dart:async';
import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';

import '../data/pheme_repository.dart';

/// Background isolate handler. Required to be a top-level, vm:entry-point
/// function. With FCM data/notification messages the system tray handles
/// display automatically, so we only need this to exist.
@pragma('vm:entry-point')
Future<void> phemeFirebaseBackgroundHandler(RemoteMessage message) async {
  // No-op: the OS displays notification-type messages while backgrounded.
}

/// Thrown when push is requested but Firebase isn't configured on this build.
class PushUnavailableException implements Exception {
  PushUnavailableException(this.message);
  final String message;
  @override
  String toString() => message;
}

/// Wraps Firebase Cloud Messaging with graceful degradation: if Firebase isn't
/// configured (no google-services.json), [available] stays false and the rest
/// of the app keeps working (in-app live updates still arrive over SSE).
class PushService {
  PushService();

  final _local = FlutterLocalNotificationsPlugin();

  bool _available = false;
  bool _initialized = false;

  bool get available => _available;

  static const _androidChannel = AndroidNotificationChannel(
    'pheme_messages',
    'Pheme messages',
    description: 'Channel notifications from Pheme',
    importance: Importance.high,
  );

  /// Best-effort initialization. Never throws; sets [available] accordingly.
  Future<void> init() async {
    if (_initialized) return;
    _initialized = true;
    try {
      await Firebase.initializeApp();
      await _local.initialize(
        settings: const InitializationSettings(
          android: AndroidInitializationSettings('@mipmap/ic_launcher'),
          iOS: DarwinInitializationSettings(),
        ),
      );
      await _local
          .resolvePlatformSpecificImplementation<
            AndroidFlutterLocalNotificationsPlugin
          >()
          ?.createNotificationChannel(_androidChannel);

      FirebaseMessaging.onBackgroundMessage(phemeFirebaseBackgroundHandler);
      FirebaseMessaging.onMessage.listen(_showForeground);

      _available = true;
    } catch (e) {
      _available = false;
      debugPrint('Pheme: push unavailable (Firebase not configured): $e');
    }
  }

  void _showForeground(RemoteMessage message) {
    final n = message.notification;
    if (n == null) return;
    _local.show(
      id: n.hashCode,
      title: n.title,
      body: n.body,
      notificationDetails: NotificationDetails(
        android: AndroidNotificationDetails(
          _androidChannel.id,
          _androidChannel.name,
          channelDescription: _androidChannel.description,
          importance: Importance.high,
          priority: Priority.high,
        ),
        iOS: const DarwinNotificationDetails(),
      ),
    );
  }

  /// Requests notification permission and registers this device's FCM token with
  /// the App API, returning the server device id. Throws
  /// [PushUnavailableException] if Firebase isn't configured.
  Future<String> registerDevice(PhemeRepository repo) async {
    if (!_available) {
      throw PushUnavailableException(
        'Push notifications are not configured (missing google-services.json).',
      );
    }
    final messaging = FirebaseMessaging.instance;
    final settings = await messaging.requestPermission();
    if (settings.authorizationStatus == AuthorizationStatus.denied) {
      throw PushUnavailableException('Notification permission was denied.');
    }
    final token = await messaging.getToken();
    if (token == null) {
      throw PushUnavailableException('Could not obtain an FCM token.');
    }
    // Handy for testing: paste this token into Firebase Console → Cloud
    // Messaging → "Send test message". Logged only in debug builds.
    debugPrint('Pheme FCM token: $token');
    final device = await repo.createDevice(
      platform: _platform,
      fcmToken: token,
    );
    return device.id;
  }

  String get _platform => Platform.isIOS ? 'ios' : 'android';
}
