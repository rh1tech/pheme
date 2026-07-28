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

  _mlsIdentityGroup();
}

/// The other way a registration goes stale: the device has an MLS identity the server was never
/// told about.
///
/// Registration happens when the app starts, which can be before the identity is minted, and
/// registerDevice attaches whatever exists at that moment. The server will not send message
/// previews to a push address it cannot trace to an MLS device — because such an address survives
/// revocation — so a device that registered a moment too early shows "New message" forever.
void _mlsIdentityGroup() {
  group('needsReregistration, MLS identity', () {
    test('an identity the server was never told about forces a re-register', () {
      expect(
        needsReregistration(
          current: 'token-a',
          registered:
              'token-a', // token unchanged, so only the identity can trigger this
          hasMlsIdentity: true,
          registeredMlsIdentity: false,
        ),
        isTrue,
      );
    });

    test('an identity already registered changes nothing', () {
      expect(
        needsReregistration(
          current: 'token-a',
          registered: 'token-a',
          hasMlsIdentity: true,
          registeredMlsIdentity: true,
        ),
        isFalse,
      );
    });

    test('no identity yet is not a reason to re-register', () {
      // Before the identity is minted there is nothing to send, and re-registering on every launch
      // until it appears would be churn for its own sake.
      expect(
        needsReregistration(
          current: 'token-a',
          registered: 'token-a',
          hasMlsIdentity: false,
          registeredMlsIdentity: false,
        ),
        isFalse,
      );
    });

    test('a missing identity link wins even when the token cannot be read', () {
      // An unreadable token normally means leave things alone, but an unlinked identity is a
      // definite, locally-known defect: previews are off until it is sent.
      expect(
        needsReregistration(
          current: null,
          registered: null,
          hasMlsIdentity: true,
          registeredMlsIdentity: false,
        ),
        isTrue,
      );
    });
  });

  group('needsReregistration, preview capability', () {
    test('a capability the server was never told about forces a re-register', () {
      // What an app upgrade looks like: the token is unchanged, the identity is linked, and the
      // only thing that moved is what this BUILD can do. iOS gaining its
      // NotificationServiceExtension is exactly this case, and without it every already-registered
      // iPhone kept the server's old `false` and never received a preview.
      expect(
        needsReregistration(
          current: 'token-a',
          registered: 'token-a',
          hasMlsIdentity: true,
          registeredMlsIdentity: true,
          canRenderPreview: true,
          registeredCanRenderPreview: false,
        ),
        isTrue,
      );
    });

    test('a capability already registered changes nothing', () {
      expect(
        needsReregistration(
          current: 'token-a',
          registered: 'token-a',
          hasMlsIdentity: true,
          registeredMlsIdentity: true,
          canRenderPreview: true,
          registeredCanRenderPreview: true,
        ),
        isFalse,
      );
    });

    test('a capability that has gone away also forces a re-register', () {
      // This direction matters more than the other. The server sends a preview data-only, so a
      // device still claiming a capability it has lost shows NOTHING — not even generic text.
      expect(
        needsReregistration(
          current: 'token-a',
          registered: 'token-a',
          canRenderPreview: false,
          registeredCanRenderPreview: true,
        ),
        isTrue,
      );
    });
  });

  // A handset that has been signed into somebody else.
  //
  // The third form this staleness has taken, and the only one where nothing about the DEVICE has
  // changed: same token, same MLS identity, same capabilities. Nothing looked stale, so no
  // registration was sent, and the server's row kept the previous account — which made it ring this
  // phone for their calls, and left the account actually signed in with no push address at all.
  group('needsReregistration, account handover', () {
    test('a handset signed into a different account must re-register', () {
      expect(
        needsReregistration(
          current: 'same-token',
          registered: 'same-token',
          currentUserId: 'xtreme',
          registeredUserId: 'medved',
        ),
        isTrue,
      );
    });

    test('the same account changes nothing', () {
      expect(
        needsReregistration(
          current: 'same-token',
          registered: 'same-token',
          currentUserId: 'xtreme',
          registeredUserId: 'xtreme',
        ),
        isFalse,
      );
    });

    test('not being signed in yet is not evidence of staleness', () {
      expect(
        needsReregistration(
          current: 'same-token',
          registered: 'same-token',
          currentUserId: '',
          registeredUserId: 'medved',
        ),
        isFalse,
      );
    });

    test('a registration whose owner was never recorded re-registers once', () {
      // The case that matters most, and the one an instinct to avoid churn gets wrong: nothing else
      // ever sends a registration again, so a handset that already holds the wrong owner can only be
      // repaired here. One POST per install, once, against a phone that otherwise rings for somebody
      // else forever.
      expect(
        needsReregistration(
          current: 'same-token',
          registered: 'same-token',
          currentUserId: 'xtreme',
          registeredUserId: null,
        ),
        isTrue,
      );
    });
  });
}
