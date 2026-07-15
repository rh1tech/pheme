import 'dart:async';

import 'package:dio/dio.dart';

import 'api_exception.dart';
import 'token_store.dart';

/// Marks a request as public: no bearer token is attached and a 401 will not
/// trigger a refresh attempt.
const Map<String, dynamic> publicRequest = {'public': true};

/// Builds the configured Dio client with transparent access-token refresh,
/// mirroring web/src/lib/api.ts. [onAuthFailure] is invoked when the session is
/// unrecoverable (refresh failed), so the app can drop to the login screen.
Dio buildDio({
  required String baseUrl,
  required TokenStore tokenStore,
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

  // A bare client (no interceptors) used only for the refresh call to avoid loops.
  final refreshDio = Dio(
    BaseOptions(
      baseUrl: baseUrl,
      headers: {'Content-Type': 'application/json'},
    ),
  );
  Completer<bool>? refreshing;

  Future<bool> refresh() async {
    if (refreshing != null) return refreshing!.future;
    final completer = Completer<bool>();
    refreshing = completer;
    try {
      final tokens = tokenStore.current;
      if (tokens == null) {
        completer.complete(false);
        return false;
      }
      final res = await refreshDio.post<Map<String, dynamic>>(
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
      refreshing = null;
    }
  }

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
          // Attempt a refresh + single retry.
          refresh().then((ok) async {
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
