// How often a conversation may be asked to catch up, and how many callers share one answer.
//
// Extracted from MlsService so it can be tested. Not for tidiness: MlsService cannot be constructed
// without the compiled Rust library, which means the one piece of this whose failure mode is a
// stampede of network requests was the one piece with no test. The logic is pure — a map, a clock
// and a duration — and it belongs somewhere a test can reach it.

import 'dart:async';

/// Coalesces repeated "we are behind, go and look" requests per key.
///
/// Two separate guards, for two separate stampedes:
///
///   * **In flight.** A feed opens with fifty messages and asks about each in turn. Every one of
///     those callers gets the SAME future, so the history is fetched once rather than fifty times.
///   * **Just finished.** Once it completes, a repeat within [gap] is answered immediately without
///     doing anything. Without this, fifty messages that are still unreadable after the catch-up
///     would queue fifty more of them, each starting as the last one cleared.
///
/// Past the gap it will genuinely try again, because the group may have moved on since — a message
/// that failed a minute ago deserves another look.
class CatchUpGate {
  CatchUpGate({this.gap = const Duration(seconds: 5), DateTime Function()? now})
    : _now = now ?? DateTime.now;

  /// How long after a completed pass a repeat is considered pointless.
  final Duration gap;

  /// Injected so a test can move time without waiting for it.
  final DateTime Function() _now;

  final _inFlight = <String, Future<void>>{};
  final _lastRun = <String, DateTime>{};

  /// Runs [work] for [key], unless a run is in flight or one finished within [gap].
  ///
  /// Never throws: a failure to catch up must not become an exception in the middle of drawing a
  /// list of messages. A failed pass still stamps the clock — otherwise an offline device would
  /// retry on every single message, which is the stampede this exists to prevent, only worse
  /// because none of them can succeed.
  Future<void> run(String key, Future<void> Function() work) {
    final inFlight = _inFlight[key];
    if (inFlight != null) return inFlight;

    final last = _lastRun[key];
    if (last != null && _now().difference(last) < gap) return Future.value();

    final future = () async {
      try {
        await work();
      } on Object {
        // Offline, or a group we are not in. Either way the message stays unread and a later pass,
        // past the gap, tries again.
      } finally {
        _lastRun[key] = _now();
        _inFlight.remove(key);
      }
    }();

    _inFlight[key] = future;
    return future;
  }

  /// Forgets everything, so the next call runs immediately. For a wipe or a sign-out.
  void reset() {
    _inFlight.clear();
    _lastRun.clear();
  }
}
