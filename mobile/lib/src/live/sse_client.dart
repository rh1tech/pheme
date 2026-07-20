import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';

import '../core/api_client.dart';
import '../models/models.dart';

/// Server-Sent Events client for the App API `/v1/stream`.
///
/// The wire format is `event: message\ndata: {LiveEvent json}\n\n` — one event name carrying four
/// different payload shapes (see [LiveEvent]). The access token goes in the query string because SSE
/// cannot carry headers.
///
/// Three things here are not incidental:
///
/// **The token is refreshed before connecting, not after failing.** The server closes the stream the
/// instant the token that opened it expires (~15 min), by design. A reconnect that reuses that same
/// dead token is closed again immediately — so the stream would die a quarter of an hour after login
/// and never come back, taking every incoming call with it.
///
/// **The backoff is jittered.** Without it, every client that was connected through a server restart
/// comes back in lockstep and knocks it over again.
///
/// **The stream is recycled after a spell in the background.** iOS severs the socket without telling
/// anyone: no error fires and the connection still reads as open. Having been away long enough is
/// the only reliable signal that it is not worth trusting.
class SseClient with WidgetsBindingObserver {
  SseClient({
    required this.baseUrl,
    required this.freshToken,
    this.onReconnect,
  });

  final String baseUrl;

  /// An access token good for at least the next couple of minutes, refreshed first if need be. Null
  /// means logged out, and there is nothing to connect with.
  final Future<String?> Function() freshToken;

  /// Called once the stream is back, so callers can refetch what they missed.
  ///
  /// The bus does not replay: no cursor, no Last-Event-ID, and it drops events for slow consumers by
  /// design. Anything sent while we were disconnected is simply gone, and the only way to see it is
  /// to go and ask over HTTP.
  final void Function()? onReconnect;

  static const _maxBackoff = Duration(seconds: 30);

  /// How long backgrounded before the stream is assumed dead on resume.
  static const _staleAfterBackground = Duration(seconds: 30);

  final _controller = StreamController<LiveEvent>.broadcast();

  /// Its own client, on the platform's HTTP stack like every other request.
  ///
  /// This one is easy to forget, because it is built here rather than handed in
  /// by [buildDio]. Forgetting it would be the worst possible outcome: the live
  /// stream is the app's LONGEST-lived connection, so it is the one an observer
  /// has the most time to look at, and it would have been the only traffic still
  /// carrying Dart's fingerprint — conspicuous precisely because everything
  /// around it had stopped.
  ///
  /// Deliberately not the shared instance from [buildDio]: that one carries
  /// `validateStatus: (_) => true` and the auth interceptor, both of which would
  /// corrupt a streamed response.
  final _dio = newNativeDio();

  final _random = Random();

  bool _closed = false;
  bool _everConnected = false;
  CancelToken? _cancel;
  DateTime? _backgroundedAt;

  Stream<LiveEvent> get events => _controller.stream;

  void start() {
    WidgetsBinding.instance.addObserver(this);
    unawaited(_connectLoop());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (_closed) return;

    if (state == AppLifecycleState.resumed) {
      final since = _backgroundedAt;
      _backgroundedAt = null;
      if (since != null &&
          DateTime.now().difference(since) >= _staleAfterBackground) {
        // It may look open and be long dead. Tear it down; the loop reconnects immediately.
        _cancel?.cancel();
      }
      return;
    }

    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden) {
      _backgroundedAt ??= DateTime.now();
    }
  }

  Future<void> _connectLoop() async {
    var attempt = 0;

    while (!_closed) {
      try {
        final token = await freshToken();
        if (token == null || token.isEmpty) {
          // Logged out — not a failure, so nothing to back off from.
          await Future<void>.delayed(const Duration(seconds: 3));
          continue;
        }

        _cancel = CancelToken();
        final res = await _dio.get<ResponseBody>(
          '$baseUrl/v1/stream',
          queryParameters: {'token': token},
          options: Options(responseType: ResponseType.stream),
          cancelToken: _cancel,
        );

        final isReconnect = _everConnected;
        _everConnected = true;
        attempt = 0;
        if (isReconnect) onReconnect?.call();

        await _readEvents(res.data!);
      } on Object {
        // Every failure is the same failure: the stream is down, so open it again with a fresh
        // token. Telling a 401 from a dropped socket would change nothing about what we do next.
      }

      if (_closed) break;
      await Future<void>.delayed(_backoff(attempt));
      attempt++;
    }
  }

  /// Exponential, capped, and jittered across the whole range, so a fleet that lost the server at
  /// the same moment does not return at the same moment.
  Duration _backoff(int attempt) {
    final exponential = 1000 * pow(2, attempt).toInt();
    final capped = min(exponential, _maxBackoff.inMilliseconds);
    return Duration(
      milliseconds: (capped * (0.5 + _random.nextDouble() / 2)).round(),
    );
  }

  Future<void> _readEvents(ResponseBody body) async {
    final lines = body.stream
        .cast<List<int>>()
        .transform(utf8.decoder)
        .transform(const LineSplitter());

    String? event;
    final data = StringBuffer();

    await for (final line in lines) {
      if (_closed) break;

      if (line.isEmpty) {
        if (event == 'message' && data.isNotEmpty) _emit(data.toString());
        event = null;
        data.clear();
        continue;
      }
      // A comment — the idle heartbeat. Its interval and its length are both
      // randomised server-side so the stream has no recognisable timing
      // signature, so nothing here may assume either.
      if (line.startsWith(':')) continue;
      if (line.startsWith('event:')) {
        event = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        data.write(line.substring(5).trim());
      }
    }
  }

  void _emit(String json) {
    try {
      _controller.add(
        LiveEvent.fromJson(jsonDecode(json) as Map<String, dynamic>),
      );
    } on FormatException {
      // A malformed frame is not worth killing the stream over.
    }
  }

  Future<void> close() async {
    _closed = true;
    WidgetsBinding.instance.removeObserver(this);
    _cancel?.cancel();
    _dio.close(force: true);
    await _controller.close();
  }
}
