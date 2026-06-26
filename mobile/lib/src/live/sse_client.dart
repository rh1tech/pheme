import 'dart:async';
import 'dart:convert';

import 'package:dio/dio.dart';

import '../models/models.dart';

/// Server-Sent Events client for the App API `/v1/stream` endpoint. The wire
/// format is `event: message\ndata: {LiveEvent json}\n\n` (see api app.go). The
/// access token is passed as a query parameter because EventSource-style
/// auth headers aren't used. Reconnects automatically with backoff.
class SseClient {
  SseClient({required this.baseUrl, required this.getToken});

  final String baseUrl;
  final Future<String?> Function() getToken;

  final _controller = StreamController<LiveEvent>.broadcast();
  final _dio = Dio();
  bool _closed = false;
  CancelToken? _cancel;

  Stream<LiveEvent> get events => _controller.stream;

  void start() {
    _connectLoop();
  }

  Future<void> _connectLoop() async {
    var backoff = const Duration(seconds: 1);
    while (!_closed) {
      try {
        final token = await getToken();
        if (token == null || token.isEmpty) {
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
        backoff = const Duration(seconds: 1); // connected: reset backoff
        await _readEvents(res.data!);
      } catch (_) {
        // fall through to backoff
      }
      if (_closed) break;
      await Future<void>.delayed(backoff);
      final next = backoff * 2;
      backoff = next > const Duration(seconds: 30)
          ? const Duration(seconds: 30)
          : next;
    }
  }

  Future<void> _readEvents(ResponseBody body) async {
    final lines = body.stream
        .cast<List<int>>()
        .transform(utf8.decoder)
        .transform(const LineSplitter());

    String? event;
    final dataBuf = StringBuffer();

    await for (final line in lines) {
      if (_closed) break;
      if (line.isEmpty) {
        // dispatch accumulated event
        if (event == 'message' && dataBuf.isNotEmpty) {
          _emit(dataBuf.toString());
        }
        event = null;
        dataBuf.clear();
        continue;
      }
      if (line.startsWith(':')) continue; // comment / heartbeat
      if (line.startsWith('event:')) {
        event = line.substring(6).trim();
      } else if (line.startsWith('data:')) {
        dataBuf.write(line.substring(5).trim());
      }
    }
  }

  void _emit(String json) {
    try {
      final map = jsonDecode(json) as Map<String, dynamic>;
      _controller.add(LiveEvent.fromJson(map));
    } catch (_) {
      // ignore malformed events
    }
  }

  Future<void> close() async {
    _closed = true;
    _cancel?.cancel();
    _dio.close(force: true);
    await _controller.close();
  }
}
