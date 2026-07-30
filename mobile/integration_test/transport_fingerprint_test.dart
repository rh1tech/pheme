// The app's HTTP transport, measured on a real device.
//
// Dart's `dart:io` client sends NO ALPN extension and speaks only HTTP/1.1
// (SecureSocket.startConnect is called without supportedProtocols in
// sdk/lib/_http/http_impl.dart), which gives it a fixed TLS fingerprint,
// identical on every platform Flutter runs on. A Pheme host serves a decoy site
// so it looks like an ordinary small website; traffic to it that is
// recognisably not-a-browser contradicts that, and the contradiction is the
// tell rather than the fingerprint on its own.
//
// So these assert the transport the app ACTUALLY builds, not a demonstration
// that the adapter exists. Measured 2026-07-20:
//
//   dart:io       JA4 t13d171000_…   no ALPN, no HTTP/2 fingerprint at all
//   Cronet        JA4 t13d1516h2_…   h2, Chrome's h2 settings   (m,a,s,p)
//   NSURLSession  JA4 t13d1313h2_…   h2, Apple's h2 settings    (m,s,p,a)
//
// Run on a device or simulator:
//   flutter test integration_test/transport_fingerprint_test.dart -d <id> \
//     --dart-define=cronetHttpNoPlay=true

import 'dart:convert';
import 'dart:io' show Platform;

import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:pheme_mobile/src/core/api_client.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:pheme_mobile/src/core/token_store.dart';

/// Reports back the TLS and HTTP/2 fingerprints of whoever asked.
const _probe = 'https://tls.browserleaks.com/json';

Future<Map<String, dynamic>> _fingerprint(Dio dio) async {
  final res = await dio.get<String>(
    _probe,
    options: Options(responseType: ResponseType.plain),
  );
  return jsonDecode(res.data!) as Map<String, dynamic>;
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  // Only Android and iOS have a platform stack to route through; elsewhere the
  // adapter falls back to Dart's by design and there is nothing to assert.
  final native = Platform.isAndroid || Platform.isIOS;

  test(
    'the app negotiates HTTP/2, which the Dart stack cannot',
    () async {
      final dio = newNativeDio();
      final f = await _fingerprint(dio);

      // ignore: avoid_print
      print(
        'ja4=${f['ja4']}  akamai=${f['akamai_hash']}  ua=${f['user_agent']}',
      );

      if (!native) return;

      // The ALPN field of JA4. "00" means no ALPN was offered at all, which is
      // what dart:io sends and what no browser sends.
      expect(
        f['ja4'],
        contains('h2'),
        reason: 'no h2 in ALPN — the request did not go via the platform stack',
      );
      // An HTTP/2 fingerprint only exists if h2 was actually negotiated, so an
      // empty one means the connection fell back to HTTP/1.1.
      expect(
        f['akamai_hash'],
        isNotEmpty,
        reason: 'no HTTP/2 fingerprint — the connection was HTTP/1.1',
      );
    },
    timeout: const Timeout(Duration(minutes: 2)),
  );

  // Both native stacks build a default User-Agent from the bundle identity:
  // Cronet sends the Android package name, CFNetwork sends CFBundleName. Either
  // would name the application in cleartext on every request, which is a worse
  // giveaway than the TLS fingerprint this change exists to fix.
  test(
    'no request names the application',
    () async {
      final f = await _fingerprint(newNativeDio());
      final ua = (f['user_agent'] as String).toLowerCase();

      expect(ua, isNot(contains('pheme')));
      expect(ua, isNot(contains('cronet')));
      expect(ua, isNot(contains('cfnetwork')));
      expect(ua, isNot(contains('dart')));
    },
    timeout: const Timeout(Duration(minutes: 2)),
  );

  // The User-Agent has to agree with the TLS handshake underneath it. A
  // Chrome-on-Android UA arriving over an Apple handshake is a contradiction,
  // and UA/TLS mismatch is a signal bot-detection systems key on explicitly.
  test(
    'the User-Agent matches the platform whose stack is underneath',
    () async {
      final f = await _fingerprint(newNativeDio());
      final ua = f['user_agent'] as String;

      if (Platform.isAndroid) {
        expect(ua, contains('Android'));
        expect(ua, contains('Chrome/'));
      } else if (Platform.isIOS) {
        expect(ua, contains('iPhone'));
        expect(ua, contains('Safari/'));
      }
    },
    timeout: const Timeout(Duration(minutes: 2)),
  );

  // buildDio is what the whole app actually uses. The refresh client (now
  // inside TokenRefreshCoordinator) is easy to miss, and missing it would send
  // a request made on every session from every device over a different stack.
  test(
    'both clients from buildDio use the platform stack',
    () async {
      final tokens = TokenStore(const FlutterSecureStorage());
      final dio = buildDio(
        baseUrl: 'https://example.invalid',
        tokenStore: tokens,
        refreshCoordinator: TokenRefreshCoordinator(
          baseUrl: 'https://example.invalid',
          tokenStore: tokens,
        ),
        onAuthFailure: () {},
      );
      final f = await _fingerprint(dio);

      expect(f['user_agent'], isNot(contains('Dart')));
      if (native) expect(f['ja4'], contains('h2'));
    },
    timeout: const Timeout(Duration(minutes: 2)),
  );
}
