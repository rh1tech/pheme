// The off-device copy: what the server is told about it, and what the person is told about it.
//
// The transcript in the recovery backup is the only copy of a decrypted history that exists off the
// device. Two things have to hold for it to be worth anything, and neither used to:
//
//   * the count sent with an upload must describe the blob sent with it, because that count is all
//     the server has to refuse a backup carrying less history than the one it already holds — it
//     cannot open the seal;
//   * a backup that is failing must look different from one that is working, because otherwise the
//     difference only shows on the day the device is gone.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/chat_cache.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';

void main() {
  group('the count the server compares backups by', () {
    test('counts every body across every conversation', () {
      expect(
        countBodies({
          'c1': {'m1': 'a', 'm2': 'b'},
          'c2': {'m3': 'c'},
        }),
        3,
      );
    });

    test('an empty transcript counts zero, not one', () {
      expect(countBodies(const {}), 0);
      // A conversation present but empty must not inflate the count: an overstated count is what
      // would let an empty backup past the guard that exists to refuse exactly that.
      expect(countBodies(const {'c1': {}}), 0);
    });

    test('the count grows with the transcript, never independently', () {
      final transcript = <String, Map<String, String>>{
        'c1': {'m1': 'a'},
      };
      expect(countBodies(transcript), 1);
      transcript['c1']!['m2'] = 'b';
      expect(countBodies(transcript), 2);
      transcript['c2'] = {'m3': 'c'};
      expect(countBodies(transcript), 3);
    });
  });

  group('backup health', () {
    test('a clean backup does not report failing', () {
      final health = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 30),
        lastError: null,
        armed: true,
      );
      expect(health.failing, isFalse);
    });

    // The case that hid a stale backup for weeks: an error recorded and nothing surfacing it.
    test('a failed attempt reports failing', () {
      const health = BackupHealth(
        lastSucceededAt: null,
        lastError: 'refused: holds less history than the stored backup',
        armed: true,
      );
      expect(health.failing, isTrue);
    });

    test('a later success clears an earlier failure', () {
      // What _runAutoBackup does: success overwrites the recorded error.
      const failed = BackupHealth(
        lastSucceededAt: null,
        lastError: 'network',
        armed: true,
      );
      final recovered = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 31),
        lastError: null,
        armed: true,
      );
      expect(failed.failing, isTrue);
      expect(recovered.failing, isFalse);
    });

    // Dormant is NOT healthy, and it is not failing either — nothing is being backed up and no
    // failure will ever be reported. The distinction matters: a device with no recovery code set up
    // reports exactly the same "not failing" as one backing up cleanly.
    test('dormant auto-backup is distinguishable from a working one', () {
      const dormant = BackupHealth(
        lastSucceededAt: null,
        lastError: null,
        armed: false,
      );
      final working = BackupHealth(
        lastSucceededAt: DateTime(2026, 7, 31),
        lastError: null,
        armed: true,
      );
      expect(dormant.failing, isFalse);
      expect(working.failing, isFalse);
      expect(
        dormant.armed,
        isFalse,
        reason:
            'nothing is sealing this device\'s history; that is not the same as being backed up',
      );
      expect(working.armed, isTrue);
    });
  });

  group('what a restore actually recovered', () {
    test('a restore that recovered nothing reports history missing', () {
      const outcome = RestoreOutcome(
        messagesRecovered: 0,
        backupHadTranscript: false,
      );
      expect(outcome.historyMissing, isTrue);
      expect(
        outcome.transcriptError,
        isNull,
        reason: 'nothing failed — the backup simply never carried a history',
      );
    });

    test(
      'a transcript that would not open is distinguishable from one absent',
      () {
        const unreadable = RestoreOutcome(
          messagesRecovered: 0,
          backupHadTranscript: true,
          transcriptError: 'bad GCM tag',
        );
        expect(unreadable.historyMissing, isTrue);
        expect(
          unreadable.transcriptError,
          isNotNull,
          reason:
              'the person needs to know the difference: one is recoverable by trying again '
              'elsewhere, the other never was',
        );
      },
    );

    test('a restore that recovered history does not report it missing', () {
      const ok = RestoreOutcome(
        messagesRecovered: 42,
        backupHadTranscript: true,
      );
      expect(ok.historyMissing, isFalse);
    });
  });
}
