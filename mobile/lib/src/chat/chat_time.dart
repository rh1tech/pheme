// Timestamps, as the web client renders them (web/src/lib/time.ts).

import 'package:intl/intl.dart';

import '../l10n/app_localizations.dart';

/// The time on a conversation-list row: today → 14:32, yesterday → "Yesterday", this week → "Tue",
/// older → 09.03.25.
String chatListTime(AppLocalizations l10n, String iso) {
  final at = DateTime.tryParse(iso)?.toLocal();
  if (at == null) return '';

  final now = DateTime.now();
  final days = _calendarDaysBetween(at, now);

  if (days == 0) return DateFormat.Hm(l10n.locale.languageCode).format(at);
  if (days == 1) return l10n.t('chat.yesterday');
  if (days < 7) return DateFormat.E(l10n.locale.languageCode).format(at);
  return DateFormat('dd.MM.yy').format(at);
}

/// The time inside a bubble. Just the clock — the day is on the separator above it.
String bubbleTime(AppLocalizations l10n, String iso) {
  final at = DateTime.tryParse(iso)?.toLocal();
  if (at == null) return '';
  return DateFormat.Hm(l10n.locale.languageCode).format(at);
}

/// The day separator pinned above each day's messages.
String dayLabel(AppLocalizations l10n, DateTime at) {
  final days = _calendarDaysBetween(at, DateTime.now());
  if (days == 0) return l10n.t('chat.today');
  if (days == 1) return l10n.t('chat.yesterday');

  final language = l10n.locale.languageCode;
  final sameYear = at.year == DateTime.now().year;
  return sameYear
      ? DateFormat.MMMMd(language).format(at)
      : DateFormat.yMMMMd(language).format(at);
}

/// The calendar day of an ISO timestamp, for grouping. Local time, because a message sent at 23:50
/// and read at 00:10 belongs to the day the sender saw, not to UTC's idea of it.
DateTime? messageDay(String iso) {
  final at = DateTime.tryParse(iso)?.toLocal();
  if (at == null) return null;
  return DateTime(at.year, at.month, at.day);
}

/// Whole calendar days between two instants — NOT a duration in hours.
///
/// 23:59 and 00:01 are one day apart even though they are two minutes apart, and that is what
/// "yesterday" has to mean or the labels are nonsense.
int _calendarDaysBetween(DateTime a, DateTime b) {
  final dayA = DateTime(a.year, a.month, a.day);
  final dayB = DateTime(b.year, b.month, b.day);
  return dayB.difference(dayA).inDays;
}
