// What the shield is allowed to claim.
//
// A status light that is wrong is worse than no status light: it converts "I don't know whether my
// messages are safe" into "I have been told they are". So the rules that map backup state onto a
// colour are worth stating explicitly, particularly the ones about when it must NOT be reassuring.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/chat_shield_status.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';

ShieldStatus status(BackupHealth backup, {bool? verified = true}) {
  // Calls the app's own decision rather than restating it. An earlier version of this file
  // duplicated the switch, which would have passed just as happily if the app had stopped
  // agreeing with it.
  return ShieldStatus(
    level: shieldLevelFor(backup, verified),
    backup: backup,
    verified: verified,
  );
}

void main() {
  group('the shield does not claim safety it cannot vouch for', () {
    // THE ONE THAT MATTERS. A device with no recovery code is backing nothing up, and that is the
    // state in which losing the handset loses every message on it — permanently, because MLS
    // destroyed the keys. It must never read as fine.
    test('no recovery code is at risk, not merely unfinished', () {
      expect(
        status(
          const BackupHealth(
            lastSucceededAt: null,
            lastError: null,
            armed: false,
          ),
        ).level,
        ShieldLevel.atRisk,
        reason:
            'nothing is being backed up; a calm colour here is a false promise',
      );
    });

    test('a failing backup is at risk even if it once succeeded', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 1),
        lastError: 'network',
        armed: true,
      );
      expect(status(health).level, ShieldLevel.atRisk);
    });

    // A failing APPEND is its own problem: the snapshot can be perfectly healthy while every new
    // message fails to reach the server, and it is the new messages that are irreplaceable.
    test('a failing append is at risk even with a healthy snapshot', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
        tailError: 'connection closed',
      );
      expect(health.failing, isTrue);
      expect(status(health).level, ShieldLevel.atRisk);
    });
  });

  group('the in-between states', () {
    test('bodies still on their way are attention, not alarm', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
        pending: 3,
      );
      expect(status(health).level, ShieldLevel.attention);
      expect(health.complete, isFalse);
    });

    test('an unverified contact is attention when the backup is fine', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
      );
      expect(status(health, verified: false).level, ShieldLevel.attention);
    });

    // Severity beats recency: a device backing nothing up does not become less urgent for also
    // having a verified contact.
    test('an unbacked-up device stays at risk even when verified', () {
      const health = BackupHealth(
        lastSucceededAt: null,
        lastError: null,
        armed: false,
      );
      expect(status(health, verified: true).level, ShieldLevel.atRisk);
    });

    // While the verification state is still loading the shield must not flash a warning it is
    // about to withdraw.
    test('an unknown verification state does not raise the level', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
      );
      expect(status(health, verified: null).level, ShieldLevel.secure);
    });
  });

  group('everything backed up', () {
    test('is secure, and paints no tint at all', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
      );
      final s = status(health);
      expect(s.level, ShieldLevel.secure);
      expect(health.complete, isTrue);
      // No wash on the ordinary case. A colour that is always there is decoration, and decoration
      // is what the eye stops seeing — which would cost the other two states their meaning.
      expect(
        s.tint(const ColorScheme.light()),
        isNull,
        reason: 'the reassuring state must not shout',
      );
    });
  });
}
