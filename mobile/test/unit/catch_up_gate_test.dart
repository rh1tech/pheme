// The gate in front of "we are behind, go and fetch the Commit history".
//
// This exists because the failure it prevents is invisible until it is expensive. A feed opens with
// fifty messages nobody can read yet; each one asks to catch up; without the gate that is fifty
// round trips to learn the same thing once, on a phone, probably on mobile data. The bug that made
// the catch-up necessary in the first place — "Not available on this device" for a message that had
// only just been sent — was fixed on a real handset and could not be tested here, because the
// service holding it cannot be built without the compiled Rust library. The gate can, and it is the
// part with a stampede for a failure mode.

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/catch_up_gate.dart';

void main() {
  group('one answer for a whole pass', () {
    test('callers that arrive while a pass is running share it', () async {
      final gate = CatchUpGate();
      var runs = 0;
      final release = Completer<void>();

      // Fifty unreadable messages, all asking at once, none of them awaited yet.
      final waiting = [
        for (var i = 0; i < 50; i++)
          gate.run('conversation', () async {
            runs++;
            await release.future;
          }),
      ];

      expect(runs, 1, reason: 'the history is fetched once, not fifty times');
      release.complete();
      await Future.wait(waiting);
      expect(runs, 1);
    });

    test('a second pass is refused until the gap has elapsed', () async {
      var clock = DateTime(2026);
      final gate = CatchUpGate(
        gap: const Duration(seconds: 5),
        now: () => clock,
      );
      var runs = 0;
      Future<void> work() async => runs++;

      await gate.run('conversation', work);
      expect(runs, 1);

      // The same pass, still unreadable afterwards, asking again immediately.
      await gate.run('conversation', work);
      expect(runs, 1, reason: 'nothing has changed in the same instant');

      clock = clock.add(const Duration(seconds: 4));
      await gate.run('conversation', work);
      expect(runs, 1, reason: 'still inside the gap');
    });

    test(
      'past the gap it tries again, because the group may have moved on',
      () async {
        var clock = DateTime(2026);
        final gate = CatchUpGate(
          gap: const Duration(seconds: 5),
          now: () => clock,
        );
        var runs = 0;
        Future<void> work() async => runs++;

        await gate.run('conversation', work);
        clock = clock.add(const Duration(seconds: 6));
        await gate.run('conversation', work);

        expect(runs, 2);
      },
    );

    test('conversations are gated independently', () async {
      final gate = CatchUpGate();
      final asked = <String>[];

      await gate.run('a', () async => asked.add('a'));
      await gate.run('b', () async => asked.add('b'));

      expect(asked, ['a', 'b'], reason: 'one slow chat must not mute another');
    });
  });

  group('a failed pass', () {
    test('does not escape — an unread message is not an exception', () async {
      final gate = CatchUpGate();

      // The device is offline. This must complete, not throw: the caller is in the middle of
      // drawing a list of messages.
      await expectLater(
        gate.run('conversation', () async => throw StateError('offline')),
        completes,
      );
    });

    test(
      'still arms the gap, so an offline device does not retry per message',
      () async {
        var clock = DateTime(2026);
        final gate = CatchUpGate(
          gap: const Duration(seconds: 5),
          now: () => clock,
        );
        var runs = 0;

        Future<void> failing() async {
          runs++;
          throw StateError('offline');
        }

        await gate.run('conversation', failing);
        await gate.run('conversation', failing);
        await gate.run('conversation', failing);

        expect(
          runs,
          1,
          reason:
              'a failure that did not stamp the clock would retry on every message — '
              'the stampede this prevents, only worse, because none of them can succeed',
        );
      },
    );
  });

  test('reset forgets the gap, for a wipe or a sign-out', () async {
    final gate = CatchUpGate(
      gap: const Duration(seconds: 5),
      now: () => DateTime(2026),
    );
    var runs = 0;
    Future<void> work() async => runs++;

    await gate.run('conversation', work);
    expect(runs, 1);

    // Signing out and back in inside the gap must not have the new identity's first catch-up
    // suppressed on behalf of the old one.
    gate.reset();
    await gate.run('conversation', work);
    expect(runs, 2);
  });
}
