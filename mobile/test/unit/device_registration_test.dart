import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/push/device_controller.dart';

/// Deciding when the server's push address for this device has gone stale.
///
/// The failure this guards against is silent on both sides. FCM rotates a token when an app is
/// reinstalled or its data cleared, and that happens while the app is not running, so onTokenRefresh
/// — which only fires in-process — never hears about it. The app comes back holding a device id it
/// trusts, never registers again, and the server keeps pushing to an address that no longer exists.
/// The phone just stays quiet, and until the server started reporting per-device failures, nothing
/// anywhere said so.
void main() {
  group('needsReregistration', () {
    test('a token that has not changed needs nothing', () {
      expect(
        needsReregistration(current: 'token-a', registered: 'token-a'),
        isFalse,
      );
    });

    test('a rotated token must be sent to the server', () {
      expect(
        needsReregistration(current: 'token-b', registered: 'token-a'),
        isTrue,
      );
    });

    test('a token the server was never told about must be sent', () {
      // The state an app upgrade lands in: registered before this check existed, so nothing was
      // ever recorded to compare against.
      expect(needsReregistration(current: 'token-a', registered: null), isTrue);
    });

    test('an unreadable token leaves the registration alone', () {
      // No Firebase, notifications declined, or Firebase having a bad moment. None of those are
      // evidence that the registration is stale, and acting as if they were would clear a working
      // one — along with the device id the call answer-lock is keyed on.
      expect(
        needsReregistration(current: null, registered: 'token-a'),
        isFalse,
      );
    });

    test(
      'an unreadable token on a device that never registered does nothing',
      () {
        expect(needsReregistration(current: null, registered: null), isFalse);
      },
    );
  });
}
