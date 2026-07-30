import 'dart:async';

import 'package:dio/dio.dart';
import 'package:native_dio_adapter/native_dio_adapter.dart';

import 'api_exception.dart';
import 'token_store.dart';
import 'user_agent.dart';

/// Shared lock for token refresh. Both the Dio interceptor and the SSE client's
/// `freshToken` callback route through here, so a concurrent 401 on a regular
/// API call and an SSE reconnect cannot trigger two simultaneous refresh calls
/// that each overwrite the other's newly-issued tokens.
class TokenRefreshCoordinator {
  TokenRefreshCoordinator({required String baseUrl, required this.tokenStore}) {
    _refreshDio = newNativeDio(
      BaseOptions(
        baseUrl: baseUrl,
        headers: {'Content-Type': 'application/json'},
      ),
    );
  }

  final TokenStore tokenStore;
  late final Dio _refreshDio;
  Completer<bool>? _refreshing;

  Future<bool> refresh() async {
    if (_refreshing != null) return _refreshing!.future;
    final completer = Completer<bool>();
    _refreshing = completer;
    try {
      final tokens = tokenStore.current;
      if (tokens == null) {
        completer.complete(false);
        return false;
      }
      final res = await _refreshDio.post<Map<String, dynamic>>(
        '/v1/auth/refresh',
        data: {'refreshToken': tokens.refreshToken},
      );
      final data = res.data;
      if (res.statusCode == 200 && data != null) {
        await tokenStore.save(
          Tokens(
            accessToken: data['accessToken'] as String,
            refreshToken: data['refreshToken'] as String,
          ),
        );
        completer.complete(true);
        return true;
      }
      await tokenStore.clear();
      completer.complete(false);
      return false;
    } catch (_) {
      await tokenStore.clear();
      completer.complete(false);
      return false;
    } finally {
      _refreshing = null;
    }
  }

  void dispose() => _refreshDio.close(force: true);
}

/// Routes a Dio instance through the platform's own HTTP stack — Cronet on
/// Android, NSURLSession on iOS and macOS — instead of Dart's.
///
/// This is a privacy change, not a performance one. Dart's `dart:io` client
/// sends NO ALPN extension and speaks only HTTP/1.1, giving it a fixed TLS
/// fingerprint identical on every platform Flutter runs on. Measured on a Pixel
/// 9 and an iOS simulator:
///
///   dart:io          JA4 t13d171000_…   no ALPN, no HTTP/2 fingerprint at all
///   Cronet           JA4 t13d1516h2_…   h2, Chrome's HTTP/2 settings (m,a,s,p)
///   NSURLSession     JA4 t13d1313h2_…   h2, Apple's HTTP/2 settings (m,s,p,a)
///
/// A Pheme host serves a decoy site, so it looks like an ordinary small website.
/// Traffic to it that is recognisably not-a-browser contradicts that, and the
/// contradiction is the tell — not the fingerprint on its own. Cronet also
/// varies its JA3 per connection the way Chrome does, where Dart's was
/// byte-identical across every run.
///
/// On any other platform the adapter falls back to the Dart stack by itself.
void _useNativeStack(Dio dio) {
  dio.httpClientAdapter = NativeAdapter();
  // Both native stacks default to a User-Agent built from the bundle identity,
  // which would name the app in cleartext on every request. See user_agent.dart.
  dio.options.headers['user-agent'] = phemeUserAgent();
}

/// A bare Dio on the platform's HTTP stack, for callers that need their own
/// client rather than the configured one from [buildDio] — the live stream,
/// which must not inherit its interceptors or `validateStatus`.
///
/// Exists so that "make a Dio" and "make a Dio that is not conspicuous" are the
/// same call, and a future client cannot quietly opt out of the transport by
/// writing `Dio()`.
Dio newNativeDio([BaseOptions? options]) {
  final dio = options == null ? Dio() : Dio(options);
  _useNativeStack(dio);
  return dio;
}

/// Marks a request as public: no bearer token is attached and a 401 will not
/// trigger a refresh attempt.
const Map<String, dynamic> publicRequest = {'public': true};

/// Builds the configured Dio client with transparent access-token refresh,
/// mirroring web/src/lib/api.ts. [onAuthFailure] is invoked when the session is
/// unrecoverable (refresh failed), so the app can drop to the login screen.
Dio buildDio({
  required String baseUrl,
  required TokenStore tokenStore,
  required TokenRefreshCoordinator refreshCoordinator,
  required void Function() onAuthFailure,
}) {
  final dio = Dio(
    BaseOptions(
      baseUrl: baseUrl,
      headers: {'Content-Type': 'application/json'},
      // Without these a stalled connection hangs the caller forever — which is what turned a slow
      // network into a Send button that spun and never stopped. A request that has not connected in
      // 15s, or gone quiet for 30s mid-response, is failed so the caller's error handling can run.
      connectTimeout: const Duration(seconds: 15),
      receiveTimeout: const Duration(seconds: 30),
      sendTimeout: const Duration(seconds: 30),
      // We translate non-2xx into ApiException ourselves.
      validateStatus: (_) => true,
    ),
  );
  _useNativeStack(dio);

  dio.interceptors.add(
    InterceptorsWrapper(
      onRequest: (options, handler) {
        if (options.extra['public'] != true) {
          final tokens = tokenStore.current;
          if (tokens != null) {
            options.headers['Authorization'] = 'Bearer ${tokens.accessToken}';
          }
        }
        handler.next(options);
      },
      onResponse: (response, handler) {
        final status = response.statusCode ?? 0;
        final isPublic = response.requestOptions.extra['public'] == true;

        if (status == 401 &&
            !isPublic &&
            response.requestOptions.extra['retried'] != true) {
          // Attempt a refresh + single retry. Uses the shared coordinator so
          // a concurrent SSE reconnect does not race against this refresh.
          refreshCoordinator.refresh().then((ok) async {
            if (!ok) {
              onAuthFailure();
              handler.reject(_authError(response.requestOptions), true);
              return;
            }
            try {
              final retried = await dio.fetch(
                response.requestOptions
                  ..extra['retried'] = true
                  ..headers['Authorization'] =
                      'Bearer ${tokenStore.current!.accessToken}',
              );
              handler.resolve(retried);
            } on DioException catch (e) {
              handler.reject(e, true);
            } catch (e) {
              // Non-Dio errors (SocketException, TimeoutException, etc.) must
              // still resolve the handler — otherwise the original caller hangs.
              handler.reject(
                DioException(
                  requestOptions: response.requestOptions,
                  error: e,
                  type: DioExceptionType.unknown,
                ),
                true,
              );
            }
          });
          return;
        }
        if (status >= 200 && status < 300) {
          handler.next(response);
          return;
        }
        if (status == 401 && !isPublic) onAuthFailure();
        handler.reject(_apiError(response), true);
      },
      onError: (e, handler) {
        // Network/transport error (timeouts, no connection, etc.).
        handler.reject(e);
      },
    ),
  );

  return dio;
}

DioException _apiError(Response response) {
  final data = response.data;
  String message = response.statusMessage ?? 'request failed';
  if (data is Map &&
      data['error'] is Map &&
      (data['error'] as Map)['message'] != null) {
    message = (data['error'] as Map)['message'].toString();
  }
  return DioException(
    requestOptions: response.requestOptions,
    response: response,
    error: ApiException(response.statusCode ?? 0, message),
    type: DioExceptionType.badResponse,
  );
}

DioException _authError(RequestOptions options) => DioException(
  requestOptions: options,
  error: AuthException(),
  type: DioExceptionType.cancel,
);
