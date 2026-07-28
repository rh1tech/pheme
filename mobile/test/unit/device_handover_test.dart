// Handing a phone from one account to another.
//
// This cost a real afternoon and rang the wrong person's phone, so it is worth stating what the bug
// actually was rather than only guarding the symptom.
//
// A push row on the server names an ACCOUNT. Signing out and in as somebody else changes the
// account and nothing else about the handset: same token, same capabilities, a freshly minted MLS
// identity sitting quietly beside the old one. Sign-out wiped everything that is chat data — keys,
// envelopes, pins, read state — and left the push registration alone, which is understandable,
// because a push registration is not chat data.
//
// What made it unrecoverable rather than merely wrong is that nothing ever registers again.
// DeviceController seeds its state from the stored device id, so ensureRegistered() returns early on
// every launch for the rest of the install's life. The registration is written once and, absent a
// staleness check, never revisited — so the handset stayed the previous account's device, and the
// server rang it for their calls.
//
// Two invariants, therefore, and they only mean anything together:
//
//   1. signing out forgets the registration, so a handover cannot carry one over;
//   2. a registration whose owner does not match the signed-in account is stale, so a handset
//      already in the wrong state repairs itself.

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/core/settings_store.dart';
import 'package:pheme_mobile/src/push/device_controller.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late SettingsStore settings;

  setUp(() {
    FlutterSecureStorage.setMockInitialValues({});
    settings = SettingsStore(const FlutterSecureStorage());
  });

  /// Everything a completed registration records.
  Future<void> registerAs(String userId) async {
    await settings.saveDeviceId('device-1');
    await settings.saveRegisteredPushToken('token-1');
    await settings.saveRegisteredMlsIdentity(true);
    await settings.saveRegisteredCanRenderPreview(true);
    await settings.saveRegisteredUserId(userId);
  }

  /// The launch-time question, asked with whatever is on disk.
  Future<bool> wouldReregisterAs(String userId) async => needsReregistration(
    current: 'token-1',
    registered: await settings.loadRegisteredPushToken(),
    hasMlsIdentity: true,
    registeredMlsIdentity: await settings.loadRegisteredMlsIdentity(),
    canRenderPreview: true,
    registeredCanRenderPreview: await settings.loadRegisteredCanRenderPreview(),
    currentUserId: userId,
    registeredUserId: await settings.loadRegisteredUserId(),
  );

  group('signing out', () {
    test('forgets the device id, so nothing is inherited', () async {
      await registerAs('medved');
      await settings.clearDeviceRegistration();

      expect(await settings.loadDeviceId(), isNull);
    });

    test('forgets which account the registration belonged to', () async {
      await registerAs('medved');
      await settings.clearDeviceRegistration();

      expect(await settings.loadRegisteredUserId(), isNull);
      expect(await settings.loadRegisteredPushToken(), isNull);
      expect(await settings.loadRegisteredMlsIdentity(), isFalse);
      expect(await settings.loadRegisteredCanRenderPreview(), isFalse);
    });

    test('leaves the next account certain to register', () async {
      await registerAs('medved');
      await settings.clearDeviceRegistration();

      expect(
        await wouldReregisterAs('xtreme'),
        isTrue,
        reason:
            'a signed-out handset must register afresh for whoever signs in next',
      );
    });
  });

  group('a handset already in the wrong state', () {
    test('repairs itself when the accounts disagree', () async {
      // No sign-out involved: this is the install that ALREADY carries the wrong owner, which is
      // the state the fleet was in when this was found.
      await registerAs('medved');

      expect(await wouldReregisterAs('xtreme'), isTrue);
    });

    test('and settles once it has', () async {
      await registerAs('medved');
      await settings.saveRegisteredUserId('xtreme');

      expect(
        await wouldReregisterAs('xtreme'),
        isFalse,
        reason: 'a correct registration must not re-send on every launch',
      );
    });
  });
}
