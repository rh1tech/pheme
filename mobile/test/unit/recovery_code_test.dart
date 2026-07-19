import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/mls_device.dart';

void main() {
  group('recovery codes', () {
    // THE BUG THIS PINS. A backup is sealed under the NORMALISED code, and the code shown to the
    // user is the pretty one — grouped with dashes. If those two are not the same bytes, a restore
    // cannot open a backup the app itself just made. Both clients had exactly that asymmetry,
    // hidden behind a retry that tried the normalised form after the raw one threw.
    test('a generated code is not its own normalised form', () {
      final pretty = generateRecoveryCode();
      expect(pretty, contains('-'));
      expect(normalizeRecoveryCode(pretty), isNot(pretty));
    });

    test('generates a 25-character code in five groups', () {
      final code = generateRecoveryCode();
      final groups = code.split('-');
      expect(groups, hasLength(5));
      for (final g in groups) {
        expect(g, hasLength(5));
      }
      expect(normalizeRecoveryCode(code), hasLength(25));
    });

    test('never repeats itself', () {
      final seen = <String>{
        for (var i = 0; i < 50; i++) generateRecoveryCode(),
      };
      expect(seen, hasLength(50));
    });

    // Everything a person might do to a code on the way back in must land on the same bytes, or
    // the backup will not open for someone who typed it correctly by eye.
    test('normalises the ways a person retypes a code', () {
      final canonical = normalizeRecoveryCode('ABCDE-FGH23-45678-9JKMN-PQRST');
      for (final variant in [
        'abcde-fgh23-45678-9jkmn-pqrst',
        'ABCDE FGH23 45678 9JKMN PQRST',
        'ABCDEFGH23456789JKMNPQRST',
        '  ABCDE-FGH23-45678-9JKMN-PQRST  ',
      ]) {
        expect(normalizeRecoveryCode(variant), canonical, reason: variant);
      }
    });

    // The ambiguous glyphs a reader cannot tell apart.
    test('folds the characters a reader cannot tell apart', () {
      expect(normalizeRecoveryCode('I'), '1');
      expect(normalizeRecoveryCode('L'), '1');
      expect(normalizeRecoveryCode('l'), '1');
      expect(normalizeRecoveryCode('O'), '0');
      expect(normalizeRecoveryCode('o'), '0');
    });

    // Applying it twice must change nothing, or a caller that normalises before calling something
    // that also normalises would derive a different key.
    test('is idempotent', () {
      final once = normalizeRecoveryCode(generateRecoveryCode());
      expect(normalizeRecoveryCode(once), once);
    });

    test('drops anything that is not part of a code', () {
      expect(normalizeRecoveryCode('AB!@#CD'), 'ABCD');
      expect(normalizeRecoveryCode(''), '');
    });

    // A generated code must survive its own normaliser without losing entropy — if the alphabet
    // contained I, L or O, normalising would collapse distinct codes onto the same bytes.
    test('generated codes never collide after normalisation', () {
      final normalised = <String>{
        for (var i = 0; i < 100; i++)
          normalizeRecoveryCode(generateRecoveryCode()),
      };
      expect(normalised, hasLength(100));
    });
  });
}
