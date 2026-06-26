import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/l10n/strings_en.dart';
import 'package:pheme_mobile/src/l10n/strings_ru.dart';

void main() {
  test('en and ru have identical key sets', () {
    final enKeys = enStrings.keys.toSet();
    final ruKeys = ruStrings.keys.toSet();
    expect(
      ruKeys.difference(enKeys),
      isEmpty,
      reason: 'ru has keys missing from en',
    );
    expect(
      enKeys.difference(ruKeys),
      isEmpty,
      reason: 'en has keys missing from ru',
    );
  });

  test('no string value is empty', () {
    for (final entry in enStrings.entries) {
      expect(entry.value.isNotEmpty, isTrue, reason: 'empty en: ${entry.key}');
    }
    for (final entry in ruStrings.entries) {
      expect(entry.value.isNotEmpty, isTrue, reason: 'empty ru: ${entry.key}');
    }
  });
}
