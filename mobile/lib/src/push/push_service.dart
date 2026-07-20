import 'dart:async';
import 'dart:convert';
import 'dart:io' show Platform;
import 'dart:ui' show DartPluginRegistrant;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_callkit_incoming/entities/entities.dart';
import 'package:flutter_callkit_incoming/flutter_callkit_incoming.dart';
import 'package:dio/dio.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../crypto/mls_device.dart';
import '../data/pheme_repository.dart';
import 'conversation_shortcuts.dart';
import 'notification_preview.dart';

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
  // A background isolate starts with NO plugins registered. Nothing here that talks to the
  // platform — drawing a notification, reading a file, opening the keystore — works until this is
  // called, and the failure is silent: the handler runs, throws inside a plugin channel, and the
  // push produces nothing at all.
  //
  // That is exactly what happened. The isolate started (FlutterFirebaseMessagingBackgroundService
  // logs it), the ringer path had never needed a Dart plugin so calls kept working, and the first
  // thing to actually need one — showing a message notification ourselves — quietly did nothing.
  DartPluginRegistrant.ensureInitialized();

  final kind = message.data['kind'];

  // A message carrying ciphertext is one the SERVER could not draw, because only this device can
  // read it. It arrives data-only for exactly that reason — a notification payload would be drawn
  // by the system tray before this handler ever ran — so if we do not draw it here, nothing does.
  //
  // That is a heavier obligation than the rest of this function has, and it is why the decrypt is
  // wrapped so tightly: every failure inside it still ends in a notification, just a generic one.
  // Note what is still NOT happening — no MLS client is loaded, no state is written. See
  // notification_preview.dart.
  if (kind == null && message.data['ciphertext'] is String) {
    await _showDecryptedInBackground(message);
    return;
  }

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

/// The Android notification channel. Declared at top level because the background isolate needs it
/// too and has no [PushService] instance to reach for.
const _androidChannel = AndroidNotificationChannel(
  'pheme_messages',
  'Pheme messages',
  description: 'Channel notifications from Pheme',
  importance: Importance.high,
);

/// Draws a message notification from the background isolate, with its body decrypted if it can be.
///
/// The generic title and body ride in the DATA rather than a notification payload, because a
/// data-only message has none — the same reason a call's caller name does. Without them a failed
/// decrypt would have nothing at all to fall back to.
@pragma('vm:entry-point')
Future<void> _showDecryptedInBackground(RemoteMessage message) async {
  final data = message.data;
  final title = (data['title'] as String?) ?? 'Pheme';
  final fallbackBody = (data['body'] as String?) ?? 'New message';
  final conversationId = (data['conversationId'] as String?) ?? '';

  String? preview;
  try {
    preview = await decryptNotificationPreview(
      conversationId: conversationId,
      ciphertextBase64: data['ciphertext'] as String?,
      groupIdsCsv: data['groupIds'] as String?,
      messageId: data['messageId'] as String?,
    );
  } on Object catch (e) {
    // Never fatal: a notification that says "New message" is a working notification.
    debugPrint('Pheme: background preview failed: $e');
  }

  final local = FlutterLocalNotificationsPlugin();
  await local.initialize(
    settings: const InitializationSettings(
      android: AndroidInitializationSettings('@mipmap/ic_launcher'),
      iOS: DarwinInitializationSettings(),
    ),
  );
  await local
      .resolvePlatformSpecificImplementation<
        AndroidFlutterLocalNotificationsPlugin
      >()
      ?.createNotificationChannel(_androidChannel);

  await _showChatNotification(
    local,
    id: _notificationIdFor(message),
    title: title,
    body: preview ?? fallbackBody,
    avatarUrl: data['senderAvatar'] as String?,
    conversationId: conversationId,
  );
}

/// The sender's avatar, as the small round icon beside a notification.
///
/// Android draws nothing unless the client supplies the bytes: the URL travels in the payload but
/// there is no field that makes the system fetch it, which is why these notifications had no
/// picture at all. FCM's own `image` field is the wrong slot — that is the hero-photograph one, and
/// an avatar put there renders full-width across the notification.
///
/// Best effort in every direction. It runs on the push path, where a slow or missing image must
/// cost the notification nothing, so it is capped in both time and size and every failure returns
/// null to draw the notification without a picture.
/// The avatar as an icon for the conversation style, or null.
Future<ByteArrayAndroidIcon?> _avatarPersonIcon(String? url) async {
  final bytes = await _avatarBytes(url);
  return bytes == null ? null : ByteArrayAndroidIcon(bytes);
}

/// The avatar as a largeIcon, for the plain notification the styled one falls back to.
Future<AndroidBitmap<Object>?> _avatarIcon(String? url) async {
  final bytes = await _avatarBytes(url);
  return bytes == null ? null : ByteArrayAndroidBitmap(bytes);
}

Future<Uint8List?> _avatarBytes(String? url) async {
  if (url == null || url.isEmpty) return null;
  try {
    final res = await Dio().get<List<int>>(
      url,
      options: Options(
        responseType: ResponseType.bytes,
        // The image endpoint is unauthenticated, so no token is needed here — which is just as
        // well, because the background isolate has no session to borrow one from.
        sendTimeout: const Duration(seconds: 3),
        receiveTimeout: const Duration(seconds: 3),
      ),
    );
    final bytes = res.data;
    // An avatar is a thumbnail. Anything past this is not one, and is not worth the memory in a
    // background isolate that the system is entitled to kill.
    if (bytes == null || bytes.isEmpty || bytes.length > 512 * 1024) {
      return null;
    }
    return Uint8List.fromList(bytes);
  } on Object catch (e) {
    debugPrint('Pheme: notification avatar unavailable: $e');
    return null;
  }
}

/// Draws a chat notification, styled as a conversation, and falls back to a plain one if that
/// fails for any reason.
///
/// The fallback is the whole point of this function existing. An earlier version drew only the
/// conversation style, and when that failed on the device the notification did not degrade — it
/// vanished. Nothing logged it, because the draw was fire-and-forget, so a phone that had been
/// showing message previews simply went quiet and stayed quiet. Appearance is worth having; it is
/// not worth a notification.
///
/// MessagingStyle is what puts the sender's avatar in the notification's own icon slot with the app
/// icon badged into its corner, rather than small and off to the right with the app in the main
/// slot. It is the difference between a notification that is about a person and one that is about
/// an app.
Future<void> _showChatNotification(
  FlutterLocalNotificationsPlugin local, {
  required int id,
  required String? title,
  required String body,
  required String? avatarUrl,
  required String? conversationId,
}) async {
  // What a tap on this notification should open. A notification the APP draws is not one Firebase
  // knows about, so onMessageOpenedApp never fires for it and the only route back to the chat is
  // this payload. Every preview is drawn by the app, so without it the notifications people
  // actually tap were the ones that went nowhere.
  final payload = (conversationId == null || conversationId.isEmpty)
      ? null
      : jsonEncode({'conversationId': conversationId});
  final groupKey = (conversationId == null || conversationId.isEmpty)
      ? null
      : conversationId;

  // Publish (or refresh) the shortcut first, so the notification about to be drawn has something
  // to anchor to. Only reaches the platform from the main isolate; from the background one this is
  // a no-op and the notification falls back to ordinary rendering.
  if (groupKey != null) {
    await ConversationShortcuts.publish(
      conversationId: groupKey,
      name: title ?? 'Chat',
      avatar: await _avatarBytes(avatarUrl),
    );
  }

  try {
    final sender = Person(
      name: title,
      // Keyed on the conversation, so Android threads successive messages from the same chat
      // together instead of treating each as a new correspondent.
      key: groupKey,
      icon: await _avatarPersonIcon(avatarUrl),
    );
    await local.show(
      id: id,
      title: title,
      body: body,
      payload: payload,
      notificationDetails: NotificationDetails(
        android: AndroidNotificationDetails(
          _androidChannel.id,
          _androidChannel.name,
          channelDescription: _androidChannel.description,
          importance: Importance.high,
          priority: Priority.high,
          groupKey: groupKey,
          // Anchors this to the conversation shortcut, which is what earns the conversation
          // treatment: avatar in the icon slot, app icon badged onto its corner. Quoting an id with
          // no published shortcut behind it is harmless — the notification just renders the
          // ordinary way, which is what it did before any of this.
          shortcutId: groupKey,
          styleInformation: MessagingStyleInformation(
            // The person this is addressed TO. Never rendered, because everything drawn here was
            // written by somebody else; it exists so Android can tell incoming from outgoing. It
            // is named anyway, because a Person with nothing set at all is a shape the platform is
            // not obliged to accept and this path has already failed silently once.
            const Person(name: 'You', key: 'self'),
            groupConversation: false,
            messages: [Message(body, DateTime.now(), sender)],
          ),
        ),
        iOS: DarwinNotificationDetails(threadIdentifier: groupKey),
      ),
    );
    return;
  } on Object catch (e) {
    debugPrint(
      'Pheme: conversation-style notification failed, falling back: $e',
    );
  }

  // Plain, with the avatar as a largeIcon. Less handsome, and it still tells the user a message
  // arrived — which is the part that matters.
  await local.show(
    id: id,
    title: title,
    body: body,
    payload: payload,
    notificationDetails: NotificationDetails(
      android: AndroidNotificationDetails(
        _androidChannel.id,
        _androidChannel.name,
        channelDescription: _androidChannel.description,
        importance: Importance.high,
        priority: Priority.high,
        largeIcon: await _avatarIcon(avatarUrl),
        groupKey: groupKey,
      ),
      iOS: DarwinNotificationDetails(threadIdentifier: groupKey),
    ),
  );
}

/// A stable, per-message notification id.
///
/// Derived from the message id where there is one, so that a retry or a duplicate delivery of the
/// same message updates its notification instead of stacking a second copy, and two different
/// messages never collide however alike their text.
@pragma('vm:entry-point')
int _notificationIdFor(RemoteMessage message) {
  final messageId = message.data['messageId'];
  final seed = (messageId is String && messageId.isNotEmpty)
      ? messageId
      : (message.messageId ?? message.hashCode.toString());
  // Positive and within 32 bits: Android notification ids are signed ints, and a negative or
  // oversized value is rejected outright on some OEM builds.
  return seed.hashCode & 0x7fffffff;
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

/// Where tapping a notification should take the user.
///
/// There are two kinds and only one of them ever worked. The tap plumbing was written for channel
/// broadcasts and looked for a channelId; a chat notification carries a conversationId and nothing
/// else, so every tap on a message from a person produced null and went nowhere. The app opened on
/// whatever screen it had been on, which reads as the tap having been ignored — and it had been.
sealed class NotificationTarget {
  const NotificationTarget();
}

/// A post in a broadcast channel.
final class ChannelMessageTarget extends NotificationTarget {
  const ChannelMessageTarget(this.channelId, this.messageId);
  final String channelId;
  final String messageId;
}

/// A conversation with a person or a group.
final class ConversationTarget extends NotificationTarget {
  const ConversationTarget(this.conversationId);
  final String conversationId;
}

/// Reads the target out of a push payload, or null if it names neither.
NotificationTarget? _targetOfData(Map<String, dynamic> data) {
  final channelId = data['channelId'];
  final messageId = data['messageId'];
  if (channelId is String &&
      channelId.isNotEmpty &&
      messageId is String &&
      messageId.isNotEmpty) {
    return ChannelMessageTarget(channelId, messageId);
  }
  final conversationId = data['conversationId'];
  if (conversationId is String && conversationId.isNotEmpty) {
    return ConversationTarget(conversationId);
  }
  return null;
}

NotificationTarget? _targetOf(RemoteMessage? message) =>
    message == null ? null : _targetOfData(message.data);

/// The target carried in a locally-drawn notification's payload.
NotificationTarget? _targetOfPayload(String? payload) {
  if (payload == null || payload.isEmpty) return null;
  try {
    final decoded = jsonDecode(payload);
    if (decoded is Map<String, dynamic>) return _targetOfData(decoded);
  } on FormatException {
    // A payload we cannot read is not worth failing a tap over; the app still opens.
  }
  return null;
}

/// Wraps Firebase Cloud Messaging with graceful degradation: if Firebase isn't
/// configured (no google-services.json), [available] stays false and the rest
/// of the app keeps working (in-app live updates still arrive over SSE).
class PushService {
  PushService();

  final _local = FlutterLocalNotificationsPlugin();
  final _taps = StreamController<NotificationTarget>.broadcast();

  bool _available = false;
  bool _initialized = false;
  NotificationTarget? _initial;

  /// Called when FCM issues this device a new token, so the server can be told. Set by the
  /// controller that owns registration; null before the app has one.
  void Function(String token)? _onTokenRefresh;

  /// Registers [handler] to receive rotated push tokens.
  // ignore: use_setters_to_change_properties
  void onTokenRefresh(void Function(String token) handler) {
    _onTokenRefresh = handler;
  }

  /// The conversation the user is currently looking at, kept in sync by the app from
  /// [activeConversationIdProvider]. A foreground message for this conversation is
  /// suppressed: it is already in the open feed over the live stream, so a second buzz
  /// on the lock screen is just noise. Null when no chat is open.
  String? activeConversationId;

  bool get available => _available;

  /// Emits when the user taps a notification while the app is running.
  Stream<NotificationTarget> get onMessageTap => _taps.stream;

  /// Returns (once) the message a notification opened the app with from a cold
  /// start, or null. Clears it so it is consumed only once.
  NotificationTarget? takeInitialMessage() {
    final ref = _initial;
    _initial = null;
    return ref;
  }

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
        onDidReceiveNotificationResponse: _onLocalNotificationTap,
      );

      // A tap that COLD-STARTED the app. The callback above only fires while this isolate is
      // alive, and the commonest case of all — phone locked, notification tapped, app launched from
      // nothing — is not that.
      final launch = await _local.getNotificationAppLaunchDetails();
      if (launch?.didNotificationLaunchApp ?? false) {
        _initial ??= _targetOfPayload(launch?.notificationResponse?.payload);
      }
      await _local
          .resolvePlatformSpecificImplementation<
            AndroidFlutterLocalNotificationsPlugin
          >()
          ?.createNotificationChannel(_androidChannel);

      FirebaseMessaging.onBackgroundMessage(phemeFirebaseBackgroundHandler);
      FirebaseMessaging.onMessage.listen(_showForeground);

      // A token is not permanent. It changes on reinstall, on a data clear, and whenever Firebase
      // decides to rotate it — and the server has no way to learn that on its own: it keeps
      // pushing to the old address and FCM answers UNREGISTERED into a void.
      //
      // That is not hypothetical. It is what happened here: the device registered once, the token
      // rotated under it, and the phone received nothing at all afterwards while everything looked
      // correctly configured from both ends.
      FirebaseMessaging.instance.onTokenRefresh.listen((token) {
        _onTokenRefresh?.call(token);
      });

      // Notification taps: while backgrounded (onMessageOpenedApp) and the tap
      // that cold-started the app (getInitialMessage).
      FirebaseMessaging.onMessageOpenedApp.listen((message) {
        final target = _targetOf(message);
        if (target != null) _taps.add(target);
      });
      _initial = _targetOf(
        await FirebaseMessaging.instance.getInitialMessage(),
      );

      _available = true;
    } catch (e) {
      _available = false;
      debugPrint('Pheme: push unavailable (Firebase not configured): $e');
    }
  }

  /// A tap on a notification this app drew itself.
  void _onLocalNotificationTap(NotificationResponse response) {
    final target = _targetOfPayload(response.payload);
    if (target != null) _taps.add(target);
  }

  void _showForeground(RemoteMessage message) {
    final n = message.notification;
    // A PREVIEW ARRIVES DATA-ONLY — no notification payload — because the tray must not draw it
    // before this app has had a chance to decrypt it. So `n` is null for exactly the messages this
    // device asked to render itself, and returning here drew nothing at all whenever the app
    // happened to be in the foreground. Backgrounded it worked, because that is a different
    // handler entirely, which is why this looked like "notifications stopped" rather than
    // "notifications stopped in one specific state".
    //
    // Calls are also data-only but carry a `kind` and are handled natively by CallKit before
    // reaching here, so they must not be drawn as banners.
    if (n == null) {
      final isMessage =
          message.data['kind'] == null && message.data['title'] is String;
      if (!isMessage) return;
    }
    // Suppress a message notification for the chat that is already on screen — the message is in the
    // open feed over the live stream, so a duplicate banner is only noise. Calls are exempt: they
    // arrive data-only (no notification payload) and never reach here, so they always ring.
    final convId = message.data['conversationId'];
    if (convId is String &&
        convId.isNotEmpty &&
        convId == activeConversationId) {
      return;
    }

    // Group by conversation, so a busy chat arrives as one expandable stack instead of ten
    // separate banners burying every other notification.
    //
    // Android has to be told here rather than by the server: FCM's AndroidNotification exposes a
    // tag (which REPLACES a notification, losing the earlier one) but no group (which bundles
    // them), so there is no server-side field to carry this. iOS is the opposite and is already
    // grouped from the server via the APNs thread-id, which is why only the Android side is set
    // below. See api/internal/push/push.go.
    final groupKey = (convId is String && convId.isNotEmpty) ? convId : null;

    // Decrypt for a preview, then draw. Done here rather than before the suppression check above,
    // so a chat already on screen costs no key material at all.
    unawaited(_showWithPreview(message, n, groupKey));
  }

  /// Draws a foreground notification, decrypting its body first if the push carried one.
  Future<void> _showWithPreview(
    RemoteMessage message,
    RemoteNotification? n,
    String? groupKey,
  ) async {
    final preview = await decryptNotificationPreview(
      conversationId: (message.data['conversationId'] as String?) ?? '',
      ciphertextBase64: message.data['ciphertext'] as String?,
      groupIdsCsv: message.data['groupIds'] as String?,
      messageId: message.data['messageId'] as String?,
    );

    await _showChatNotification(
      _local,
      // Keyed on the MESSAGE, not the notification object. n.hashCode is derived from the payload,
      // so two messages with identical text — "ok", twice — would collide and the second would
      // silently replace the first.
      id: _notificationIdFor(message),
      // The data copies are the ONLY source when the push came data-only, which is every push
      // carrying a preview. The server sends both for exactly this reason.
      title: n?.title ?? message.data['title'] as String?,
      // The server's generic body when there is no preview: no key material here, previews turned
      // off, or a decrypt that did not land. All of them still deserve a notification.
      body:
          preview ??
          n?.body ??
          message.data['body'] as String? ??
          'New message',
      avatarUrl: message.data['senderAvatar'] as String?,
      conversationId: groupKey,
    );
  }

  /// Registers this device with the server and returns its id, attaching push tokens if we can get
  /// them.
  ///
  /// It registers EVEN WITHOUT PUSH, and that is the point. The device id is not only a push address:
  /// it is what the call answer-lock is keyed on. Every device the user is signed in on rings, and the
  /// server decides which one may pick up — by this id. So a user who declined notifications, or a Mac
  /// with no Firebase, still needs one, or their phone rings and the Answer button does nothing.
  ///
  /// It used to throw in all three of those cases.
  /// The push token this device currently holds, or null if it has none.
  ///
  /// Deliberately does NOT ask for permission — this is called on launch to check whether the
  /// address the server has for us is still the right one, and prompting for that would put a
  /// permission dialog in front of someone who has already answered the question once.
  Future<String?> currentPushToken() async {
    if (!_available) return null;
    try {
      return await FirebaseMessaging.instance.getToken();
    } on Object {
      // A token we cannot read is indistinguishable from one that has not changed. Reporting null
      // leaves the existing registration alone, which is the safe direction: the alternative is
      // re-registering on every launch whenever Firebase is briefly unavailable.
      return null;
    }
  }

  Future<Registration> registerDevice(PhemeRepository repo) async {
    String? fcmToken;
    String? voipToken;

    if (_available) {
      final messaging = FirebaseMessaging.instance;
      final settings = await messaging.requestPermission();

      // A denial costs the user their notifications. It must not also cost them the ability to answer
      // a call, so we carry on and register without a token.
      if (settings.authorizationStatus != AuthorizationStatus.denied) {
        fcmToken = await messaging.getToken();
        if (fcmToken != null) {
          // Handy for testing: paste this into Firebase Console → Cloud Messaging → "Send test
          // message". Debug builds only.
          debugPrint('Pheme FCM token: $fcmToken');
        }
      }

      // The iOS PushKit token, which is NOT the FCM token — a different token for a different topic,
      // and the only one that can ring a sleeping iPhone. Both are sent: FCM carries messages, PushKit
      // carries calls. Null everywhere but iOS.
      voipToken = await _voipToken();
    }

    // Best effort: a device that has not minted an MLS identity yet still needs a push address,
    // and will send the link on its next registration.
    final mlsDeviceId = await loadMlsDeviceId(const FlutterSecureStorage());

    final device = await repo.createDevice(
      platform: _platform,
      fcmToken: fcmToken,
      voipToken: voipToken,
      mlsDeviceId: mlsDeviceId,
      canRenderPreview: _canRenderPreview,
    );
    return Registration(
      id: device.id,
      pushToken: fcmToken,
      // Whether the MLS identity actually made it into this registration. Best-effort above, so it
      // is genuinely absent when the identity has not been minted yet — and the server withholds
      // previews from an address it cannot trace to one.
      linkedMlsIdentity: mlsDeviceId != null && mlsDeviceId.isNotEmpty,
    );
  }

  /// Whether THIS build can decrypt a message and draw the notification itself.
  ///
  /// Android only, for now, and the asymmetry is real rather than an oversight. Android renders a
  /// preview from the FCM background isolate (see phemeFirebaseBackgroundHandler), which exists.
  /// iOS would need a NotificationServiceExtension, which does not — so an iPhone says no and
  /// keeps getting the server's generic text, which is correct and not a degradation: it is
  /// exactly what it got before.
  ///
  /// Claiming the capability before it exists would be worse than not having it. The server sends
  /// a preview data-only, and a device that cannot draw one shows NOTHING — so a premature `true`
  /// here would silence every notification on iOS.
  ///
  /// Flip this to `Platform.isAndroid || Platform.isIOS` in the same change that adds the
  /// extension, never before it.
  bool get _canRenderPreview => Platform.isAndroid;

  /// This device's PushKit token, or null when there is none (Android, or iOS before it has been
  /// issued). Best effort: a missing VoIP token degrades an incoming call to a banner, which is worse
  /// than a call screen but a great deal better than refusing to register the device at all.
  Future<String?> _voipToken() async {
    if (!Platform.isIOS) return null;
    try {
      final token = await FlutterCallkitIncoming.getDevicePushTokenVoIP();
      return (token == null || token.isEmpty) ? null : token;
    } on Object {
      return null;
    }
  }

  /// What this device tells the server it is.
  ///
  /// This used to be `isIOS ? 'ios' : 'android'`, which reports ANDROID on a Mac — and that is not
  /// merely untidy: the server routes a call to PushKit for an iOS device with a VoIP token, and
  /// decides what a push may carry from the platform. A device lying about what it is gets the wrong
  /// treatment for the rest of its life.
  String get _platform {
    if (Platform.isIOS) return 'ios';
    if (Platform.isMacOS) return 'macos';
    return 'android';
  }
}

/// What a registration produced: the server's id for this device, and the push token it was told
/// about.
///
/// The token comes back so the caller can remember what the server was told. That is the only way a
/// later launch can notice the token has since changed — FCM rotates them, and a rotation that
/// happens while the app is closed raises no event the app can hear.
///
/// A null token is normal rather than a failure: a Mac has no Firebase, and someone who declined
/// notifications still gets an id, because the id is also what lets them answer a call.
final class Registration {
  const Registration({
    required this.id,
    this.pushToken,
    this.linkedMlsIdentity = false,
  });

  final String id;
  final String? pushToken;

  /// Whether this registration told the server which MLS device the address belongs to. False means
  /// the server cannot revoke this address, and so will not send it message previews.
  final bool linkedMlsIdentity;
}
