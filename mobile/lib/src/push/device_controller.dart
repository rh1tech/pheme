import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../crypto/mls_device.dart';

import '../core/providers.dart';

/// This device's server-issued id.
///
/// Two things need it, and they are NOT the same thing, which is why registering a device and enabling
/// push are now separate:
///
///   * PUSH. Needs a token, which needs Firebase and the user's permission. Optional: the app works
///     without it, over the live stream.
///   * THE CALL ANSWER-LOCK. Needs only the id. Every device the user is signed in on rings, and
///     exactly one may pick up — the server decides which, keyed on this id.
///
/// They used to be one call, and the consequence was that a user who declined notifications, or a Mac
/// with no Firebase, had no device id — and therefore could not ANSWER A CALL. The phone rang and the
/// button did nothing. Registering now always happens; the push token is attached when there is one.
class DeviceController extends Notifier<String?> {
  @override
  String? build() => ref.read(initialAppStateProvider).deviceId;

  bool get isRegistered => state != null;

  /// Registers this device with the server, with a push token if we can get one and without if we
  /// cannot. Returns the device id.
  Future<String> register() async {
    // A rotated token has to reach the server, and the only way it does is by registering again.
    // See PushService.onTokenRefresh — without this the app holds a device id forever and never
    // notices that the address behind it stopped working.
    ref.read(pushServiceProvider).onTokenRefresh((_) {
      state =
          null; // force the next ensureRegistered to re-register with the new token
      unawaited(ensureRegistered());
    });

    final existing = state;
    if (existing != null) return existing;

    final registration = await ref
        .read(pushServiceProvider)
        .registerDevice(ref.read(repositoryProvider));

    final settings = ref.read(settingsStoreProvider);
    await settings.saveDeviceId(registration.id);
    // Remember what the server was told, so a later launch can tell whether it is still true.
    final token = registration.pushToken;
    if (token != null) {
      await settings.saveRegisteredPushToken(token);
    }
    await settings.saveRegisteredMlsIdentity(registration.linkedMlsIdentity);
    // What this build told the server it can do, so a later launch can notice the answer has
    // changed — which is what an app upgrade does without touching anything else.
    await settings.saveRegisteredCanRenderPreview(
      ref.read(pushServiceProvider).canRenderPreview,
    );
    state = registration.id;
    return registration.id;
  }

  /// Re-registers so the server learns which MLS device this push address belongs to.
  ///
  /// Called the moment an identity is minted (see MlsService.onIdentityMinted), because that is the
  /// one event that makes an existing registration stale without anything else changing. The launch
  /// check below covers the same mismatch, but only at launch — so before this existed, a fresh
  /// install linked its identity on the SECOND run of the app, and previews did not work until the
  /// user happened to restart. Nothing reported it; the phone just said "New message" forever.
  ///
  /// Safe to call spuriously: it re-reads what the server was actually told and returns without a
  /// request when the link is already recorded. Minting fires on restores and re-mints too, and
  /// those must not each cost a registration.
  Future<void> linkMlsIdentity() async {
    final settings = ref.read(settingsStoreProvider);
    final mlsDeviceId = await loadMlsDeviceId(const FlutterSecureStorage());

    // The same rule the launch check uses, deliberately — one statement of when a registration is
    // stale, in one tested place, rather than a second copy here that can drift from it. Passing a
    // null token says "the token is not what I am asking about": with both sides null the token
    // comparison cannot fire, so the answer turns purely on the identity link.
    if (!needsReregistration(
      current: null,
      registered: null,
      hasMlsIdentity: mlsDeviceId != null && mlsDeviceId.isNotEmpty,
      registeredMlsIdentity: await settings.loadRegisteredMlsIdentity(),
    )) {
      return;
    }

    // Past register()'s early return: the device id is fine, but what the server knows about it
    // is not, and only a fresh registration carries the link.
    state = null;
    await ensureRegistered();
  }

  /// Re-registers if this device's push token has changed since the server was last told.
  ///
  /// onTokenRefresh only fires while the app is RUNNING. FCM also rotates a token when the app is
  /// reinstalled or its data cleared, and those happen while nothing is listening — so the app comes
  /// back holding a device id it trusts, never registers again, and the server keeps pushing to an
  /// address that has stopped existing. Nothing on either side reports it: the server's send fails
  /// per-device, and the phone simply stays quiet.
  ///
  /// Comparing on launch is what closes that gap. It costs one token read and, in the ordinary case
  /// where nothing has changed, no request at all.
  Future<void> refreshRegistrationIfTokenChanged() async {
    final token = await ref.read(pushServiceProvider).currentPushToken();
    final settings = ref.read(settingsStoreProvider);
    final registered = await settings.loadRegisteredPushToken();
    final mlsDeviceId = await loadMlsDeviceId(const FlutterSecureStorage());
    final registeredMls = await settings.loadRegisteredMlsIdentity();
    final hasMls = mlsDeviceId != null && mlsDeviceId.isNotEmpty;
    if (!needsReregistration(
      current: token,
      registered: registered,
      hasMlsIdentity: hasMls,
      registeredMlsIdentity: registeredMls,
      canRenderPreview: ref.read(pushServiceProvider).canRenderPreview,
      registeredCanRenderPreview: await settings
          .loadRegisteredCanRenderPreview(),
    )) {
      return;
    }

    // Force register() past its early return: the id may still be valid, but the address behind it
    // is not, and only a fresh registration carries the new token.
    state = null;
    await ensureRegistered();
  }

  /// Makes sure this device has an id, without asking for notification permission.
  ///
  /// Called before a call can be answered. It must not prompt: being asked for notification permission
  /// by a phone that is already ringing would be absurd, and declining it would silently make the call
  /// unanswerable.
  Future<String?> ensureRegistered() async {
    final existing = state;
    if (existing != null) return existing;

    try {
      return await register();
    } on Object {
      return null;
    }
  }

  /// True where push can work at all. macOS has no FCM in this app and no PushKit anywhere, so a Mac
  /// hears about a call over the live stream — which is the honest arrangement for a machine that is
  /// either open or off.
  bool get pushSupported => !Platform.isMacOS;
}

/// Whether the server's push address for this device needs replacing.
///
/// Pulled out as a plain function because the rule is easy to get subtly wrong in the direction
/// that costs somebody their notifications, and the surrounding controller cannot be tested without
/// Firebase.
///
/// A null [current] means the token could not be read — no Firebase, notifications declined, or a
/// transient failure. That is NOT evidence the registration is stale, and treating it as such would
/// clear a perfectly good one every time Firebase had a bad moment. Worse, the device id is also
/// what the call answer-lock is keyed on, so churning it costs more than a notification.
///
/// A null [registered] with a real [current] does mean re-registering: it is a device that has a
/// token the server was never told about, which is exactly the state an app upgrade lands in.
bool needsReregistration({
  required String? current,
  required String? registered,
  bool hasMlsIdentity = false,
  bool registeredMlsIdentity = false,
  bool canRenderPreview = false,
  bool registeredCanRenderPreview = false,
}) {
  // An MLS identity this device has but the server was never told about is the other way a
  // registration goes stale, and it is easy to reach: the device registers when the app starts,
  // which can be before the MLS identity has been minted, and registerDevice attaches whatever
  // exists at that moment. The comment there says the link is sent "on its next registration" —
  // but with register() returning early forever, there was no next one.
  //
  // The consequence is not a missing link, it is missing PREVIEWS. The server will not hand
  // ciphertext to a push address it cannot trace to an MLS device, because such an address cannot
  // be revoked. So the device shows "New message" and nothing else, permanently, with nothing
  // anywhere reporting a fault.
  if (hasMlsIdentity && !registeredMlsIdentity) return true;

  // A capability the BUILD has and the server does not know about. Same shape of staleness as the
  // identity link above, reached a different way: not by the device changing, but by the app being
  // upgraded underneath a registration that is otherwise perfectly valid.
  //
  // This is not hypothetical — it is what happened when iOS gained its NotificationServiceExtension.
  // The app started reporting canRenderPreview: true, every already-registered iPhone kept the
  // `false` the server had recorded, and the server went on withholding ciphertext from devices
  // that could now decrypt it. Nothing was broken and nothing said anything; the previews simply
  // never arrived.
  //
  // Compared in BOTH directions on purpose. A capability that goes away — an OS downgrade, an
  // entitlement lost when a build is signed differently — matters more than one that appears: the
  // server sends previews data-only, so a device still claiming a capability it no longer has shows
  // NOTHING at all rather than generic text.
  if (canRenderPreview != registeredCanRenderPreview) return true;

  if (current == null) return false;
  return current != registered;
}
