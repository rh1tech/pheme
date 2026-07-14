import 'dart:async';
import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_callkit_incoming/entities/entities.dart';
import 'package:flutter_callkit_incoming/flutter_callkit_incoming.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';

import '../data/pheme_repository.dart';

/// Background isolate handler. Must be a top-level, vm:entry-point function.
///
/// For an ordinary message this stays a no-op: the OS renders notification-type messages itself while
/// the app is backgrounded, and there is nothing to add.
///
/// For a CALL it does exactly one thing — raise the ringer — and nothing else.
///
/// That restraint is the whole design. This runs in a SEPARATE ISOLATE from the app, with its own
/// memory: no providers, no MLS client, no session. It could not decrypt the invite if it tried, and
/// trying would mean standing up a second MLS client against the same key store, which is precisely
/// the race the single-client rule exists to prevent. So it rings, and every scrap of real work — the
/// mailbox, the epoch catch-up, the key, the SDP — happens on the root isolate once the user answers
/// and the app comes to the foreground.
///
/// This is also why the server sends a call as a DATA-ONLY high-priority message. A message with a
/// notification payload is drawn by the system tray and does not reliably start this handler at all —
/// so the phone would show a banner and never ring.
@pragma('vm:entry-point')
Future<void> phemeFirebaseBackgroundHandler(RemoteMessage message) async {
  final kind = message.data['kind'];
  if (kind != 'call' && kind != 'call-cancel') return;

  final callId = message.data['callId'];
  if (callId is! String || callId.isEmpty) return;

  if (kind == 'call-cancel') {
    // The caller gave up before we answered. Take the ring off the lock screen, or it sits there
    // looking live and deep-links into a call nobody is on.
    await FlutterCallkitIncoming.endCall(callId);
    return;
  }

  final conversationId = (message.data['conversationId'] as String?) ?? '';
  // The name comes from the DATA and not from the notification title, because a data-only message has
  // no title. A name left there would reach nobody and the phone would ring for a stranger.
  final callerName = (message.data['callerName'] as String?) ?? 'Pheme';

  await FlutterCallkitIncoming.showCallkitIncoming(
    CallKitParams(
      id: callId,
      nameCaller: callerName,
      appName: 'Pheme',
      handle: conversationId,
      type: 0,
      duration: 35000,
      extra: {'conversationId': conversationId},
      android: const AndroidParams(
        isCustomNotification: true,
        isShowLogo: false,
        isShowFullLockedScreen: true,
        ringtonePath: 'system_ringtone_default',
      ),
      ios: const IOSParams(supportsVideo: false, audioSessionMode: 'voiceChat'),
    ),
  );
}

/// Thrown when push is requested but Firebase isn't configured on this build.
class PushUnavailableException implements Exception {
  PushUnavailableException(this.message);
  final String message;
  @override
  String toString() => message;
}

/// Identifies the message a tapped notification should open.
class MessageRef {
  const MessageRef(this.channelId, this.messageId);
  final String channelId;
  final String messageId;
}

/// Extracts a [MessageRef] from a push message's data, or null if it lacks the
/// channelId/messageId the server injects.
MessageRef? _refOf(RemoteMessage? message) {
  if (message == null) return null;
  final cid = message.data['channelId'];
  final mid = message.data['messageId'];
  if (cid is String && cid.isNotEmpty && mid is String && mid.isNotEmpty) {
    return MessageRef(cid, mid);
  }
  return null;
}

/// Wraps Firebase Cloud Messaging with graceful degradation: if Firebase isn't
/// configured (no google-services.json), [available] stays false and the rest
/// of the app keeps working (in-app live updates still arrive over SSE).
class PushService {
  PushService();

  final _local = FlutterLocalNotificationsPlugin();
  final _taps = StreamController<MessageRef>.broadcast();

  bool _available = false;
  bool _initialized = false;
  MessageRef? _initial;

  bool get available => _available;

  /// Emits when the user taps a notification while the app is running.
  Stream<MessageRef> get onMessageTap => _taps.stream;

  /// Returns (once) the message a notification opened the app with from a cold
  /// start, or null. Clears it so it is consumed only once.
  MessageRef? takeInitialMessage() {
    final ref = _initial;
    _initial = null;
    return ref;
  }

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

      // Notification taps: while backgrounded (onMessageOpenedApp) and the tap
      // that cold-started the app (getInitialMessage).
      FirebaseMessaging.onMessageOpenedApp.listen((message) {
        final ref = _refOf(message);
        if (ref != null) _taps.add(ref);
      });
      _initial = _refOf(await FirebaseMessaging.instance.getInitialMessage());

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
    // The iOS PushKit token, which is NOT the FCM token — it is a different token for a different
    // topic, and it is the only one that can ring a sleeping iPhone. Both are sent: FCM carries
    // messages, PushKit carries calls. Empty on Android, where a data-only high-priority FCM message
    // does the job.
    final voipToken = await _voipToken();

    final device = await repo.createDevice(
      platform: _platform,
      fcmToken: token,
      voipToken: voipToken,
    );
    return device.id;
  }

  /// This device's PushKit token, or null when there is none (Android, or iOS before it has been
  /// issued). Best effort: a missing VoIP token degrades an incoming call to a banner, which is worse
  /// than a call screen but a great deal better than refusing to register the device at all.
  Future<String?> _voipToken() async {
    if (!Platform.isIOS) return null;
    try {
      final token = await FlutterCallkitIncoming.getDevicePushTokenVoIP();
      return (token == null || token.isEmpty) ? null : token as String;
    } on Object {
      return null;
    }
  }

  String get _platform => Platform.isIOS ? 'ios' : 'android';
}
