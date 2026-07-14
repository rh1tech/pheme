// The three states of "is this device in the encrypted group", and why there have to be three.
//
// There used to be two. `joined` was a bool that started false, so from the first frame until the
// network came back the app told the user encryption was still being set up — every time a chat was
// opened, on a device that had been holding the keys for weeks. Nothing was being set up. The device
// was waiting to be told a group id it already knew.
//
// The bug was not the banner. The bug was that the model could not express "I have not asked yet",
// so the UI had to guess, and it guessed the alarming answer.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/message_feed_controller.dart';

void main() {
  group('the joining notice', () {
    test(
      'says NOTHING while we are still working out whether we are in the group',
      () {
        // The state a chat is in for the first few hundred milliseconds, every single time it opens.
        const opening = MessageFeedState();
        expect(opening.joined, isNull, reason: 'unknown, not false');
        expect(
          feedNoticeKey(opening),
          isNull,
          reason:
              'the old bool started false and this is exactly where it lied',
        );
      },
    );

    test('says nothing once we know we ARE in the group', () {
      const joined = MessageFeedState(joined: true, loading: false);
      expect(feedNoticeKey(joined), isNull);
    });

    // The one case the banner is actually FOR: a device that has signed in and is genuinely waiting
    // for a member to admit it. It cannot read what was said before it arrived, and saying so beats a
    // composer that silently refuses to send.
    test('says so once we know we are NOT in the group', () {
      const notJoined = MessageFeedState(joined: false, loading: false);
      expect(feedNoticeKey(notJoined), 'chat.joiningOnThisDevice');
    });

    // A different problem with a different resolution: this one is the other person's to fix, and no
    // amount of waiting resolves it.
    test('a peer with no keys wins over everything else', () {
      const peerNotReady = MessageFeedState(
        joined: false,
        peerNotReady: true,
        loading: false,
      );
      expect(feedNoticeKey(peerNotReady), 'chat.peerNotReady');
    });
  });

  group('copyWith', () {
    test('can move joined from unknown to a real answer', () {
      const unknown = MessageFeedState();
      expect(unknown.copyWith(joined: true).joined, isTrue);
      expect(unknown.copyWith(joined: false).joined, isFalse);
    });

    // Switching conversations must not leave the previous chat's answer behind — a stale "yes" is the
    // same bug pointing the other way.
    test('can put joined back to unknown', () {
      const joined = MessageFeedState(joined: true);
      expect(joined.copyWith(clearJoined: true).joined, isNull);
    });

    test('leaves joined alone when it is not being set', () {
      const joined = MessageFeedState(joined: true);
      expect(joined.copyWith(loading: false).joined, isTrue);
    });
  });
}
