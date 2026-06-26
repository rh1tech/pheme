import 'package:intl/intl.dart';

final DateFormat _dateTime = DateFormat.yMMMd().add_jm();
final DateFormat _date = DateFormat.yMMMd();

/// Formats an ISO-8601 timestamp (as returned by the API) for display. Falls
/// back to the raw string if it can't be parsed.
String formatDateTime(String iso) {
  final parsed = DateTime.tryParse(iso);
  if (parsed == null) return iso;
  return _dateTime.format(parsed.toLocal());
}

String formatDate(String iso) {
  final parsed = DateTime.tryParse(iso);
  if (parsed == null) return iso;
  return _date.format(parsed.toLocal());
}
