// The one judgement a push preview has to make: whether the name on the banner can be trusted.
//
// A notification is titled with a NAME, and the only source for that name is the push payload —
// which the server composes, and the server is the untrusted Delivery Service in MLS. It can attach
// any name to any ciphertext it relays. The ciphertext itself cannot be faked: MLS authenticates the
// leaf that signed it, and `decryptNotificationPreview` carries that identity back out.
//
// When the two disagree, the message is real and everything the payload said about WHO is not. The
// lock screen is exactly where nobody looks twice, so the rule is pinned here.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/push/notification_preview.dart';

void main() {
  group('NotificationPreview.contradicts', () {
    test('a payload naming somebody else than the signature is caught', () {
      const preview = NotificationPreview(
        body: 'the real message',
        senderUserId: 'bob',
      );
      expect(preview.contradicts('alice'), isTrue);
    });

    test('an agreeing payload is not a contradiction', () {
      const preview = NotificationPreview(
        body: 'the real message',
        senderUserId: 'alice',
      );
      expect(preview.contradicts('alice'), isFalse);
    });

    test(
      'a payload that claims no sender cannot retain its title or avatar',
      () {
        const preview = NotificationPreview(
          body: 'the real message',
          senderUserId: 'bob',
        );
        expect(preview.contradicts(null), isTrue);
        expect(preview.contradicts(''), isTrue);
      },
    );

    test('a body with no authenticated sender is shown neutrally', () {
      // A preview served from a LEGACY cache entry, written before senders were stored. There is no
      // cryptographic answer for it and there never can be — the MLS key is long gone.
      const legacy = NotificationPreview(
        body: 'from an older build',
        senderUserId: '',
      );
      expect(legacy.contradicts('alice'), isTrue);
    });
  });
}
