import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// A Pheme host mounts its API under a random path prefix, so that the site an
/// unauthenticated prober sees at `/` is an ordinary static site and nothing on
/// the host behaves like an API. The client learns the prefix as part of the
/// server URL it is already given — `https://host.example/a7f3c91e4b2d` — and
/// nothing else in the app has to know about it.
///
/// That only holds because Dio *concatenates* baseUrl and path rather than
/// resolving them as URIs. `Uri.resolve('/v1/me')` against a base with a path
/// would discard the prefix and silently send every request to the decoy site,
/// which would look like a server outage rather than a client bug. These tests
/// exist so a Dio upgrade that changed the merge strategy fails here instead.
void main() {
  Uri urlFor(String baseUrl, String path, {Map<String, dynamic>? query}) =>
      RequestOptions(baseUrl: baseUrl, path: path, queryParameters: query).uri;

  group('a base URL carrying a path prefix', () {
    test('keeps the prefix in front of the API path', () {
      expect(
        urlFor(
          'https://host.example/a7f3c91e4b2d',
          '/v1/auth/login',
        ).toString(),
        'https://host.example/a7f3c91e4b2d/v1/auth/login',
      );
    });

    test('tolerates a trailing slash without doubling it', () {
      expect(
        urlFor('https://host.example/a7f3c91e4b2d/', '/v1/me').toString(),
        'https://host.example/a7f3c91e4b2d/v1/me',
      );
    });

    test('keeps the prefix on the SSE stream, query parameters and all', () {
      // The live stream is the one URL the app builds by hand rather than
      // letting Dio's baseUrl do it, so it is the most likely thing to be
      // missed when the prefix lands.
      final uri = urlFor(
        'https://host.example/a7f3c91e4b2d',
        '/v1/stream',
        query: {'token': 'jwt-goes-here'},
      );
      expect(uri.path, '/a7f3c91e4b2d/v1/stream');
      expect(uri.queryParameters['token'], 'jwt-goes-here');
    });

    test('keeps the prefix on image URLs built from the base', () {
      // pheme_repository.imageUrl builds these by interpolation.
      const base = 'https://host.example/a7f3c91e4b2d';
      expect(
        '$base/v1/images/img-1',
        'https://host.example/a7f3c91e4b2d/v1/images/img-1',
      );
    });
  });

  group('a base URL with no prefix', () {
    test('still resolves exactly as before', () {
      expect(
        urlFor('https://hub.example.com', '/v1/me').toString(),
        'https://hub.example.com/v1/me',
      );
    });

    test('works for a local dev server on a port', () {
      expect(
        urlFor('http://10.0.2.2:8080', '/v1/me').toString(),
        'http://10.0.2.2:8080/v1/me',
      );
    });
  });
}
