import 'package:flutter/material.dart';

import 'strings_en.dart';
import 'strings_ru.dart';

/// Lightweight localization (English + Russian) mirroring the web app's two
/// languages, without code generation. Look up strings via [t] / [tp].
class AppLocalizations {
  AppLocalizations(this.locale)
    : _strings = locale.languageCode == 'ru' ? ruStrings : enStrings;

  final Locale locale;
  final Map<String, String> _strings;

  static const supportedLocales = [Locale('en'), Locale('ru')];

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  static AppLocalizations of(BuildContext context) =>
      Localizations.of<AppLocalizations>(context, AppLocalizations)!;

  String t(String key) => _strings[key] ?? key;

  /// Like [t] but replaces `{name}` style placeholders with [params].
  String tp(String key, Map<String, String> params) {
    var value = t(key);
    params.forEach((k, v) => value = value.replaceAll('{$k}', v));
    return value;
  }
}

/// Ergonomic access: `context.l10n.t('key')` from any widget below MaterialApp.
extension AppLocalizationsX on BuildContext {
  AppLocalizations get l10n => AppLocalizations.of(this);
}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  bool isSupported(Locale locale) => AppLocalizations.supportedLocales.any(
    (l) => l.languageCode == locale.languageCode,
  );

  @override
  Future<AppLocalizations> load(Locale locale) async =>
      AppLocalizations(locale);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}
