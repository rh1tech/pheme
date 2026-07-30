// The wire bodies of the signed history handoff, and the rules for refusing one.
//
// The signature itself is made and checked in Rust; what this app owns is everything around it —
// which bodies it will even look at, and the second, independent check that the identity a body
// claims matches the poster the server authenticated. Those are the rules that decide whether a
// transcript of somebody else's invention may land on a fresh device, so they are tested on their
// own, without the Rust library.
//
// The last group reads test/fixtures/mls_history_vectors.json — the SAME file the Rust suite
// (crates/pheme-mls/tests/history_vectors.rs) and the web suite (web/test/mlsHistory.test.ts) read.
// Web and mobile reach one canonical transcript through their bindings, so the bytes cannot drift;
// what could drift is the body each client writes those bytes from, and that is what the fixture
// pins for all three.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/attribution.dart';
import 'package:pheme_mobile/src/crypto/history_handoff.dart';

const requester = 'mimi://test.example/d/bob/dev-b';
const offerer = 'mimi://test.example/d/alice/dev-a';

Uint8List body(Map<String, dynamic> json) =>
    Uint8List.fromList(utf8.encode(jsonEncode(json)));

Map<String, dynamic> requestJson([Map<String, dynamic> overrides = const {}]) =>
    {
      'v': 2,
      'id': requester,
      'epoch': 4,
      'nonce': 'AQIDBA==',
      'sig': 'c2ln',
      ...overrides,
    };

Map<String, dynamic> offerJson([Map<String, dynamic> overrides = const {}]) => {
  'v': 2,
  'from': offerer,
  'to': requester,
  'epoch': 4,
  'historyId': 'hist-1',
  'salt': 'c2FsdA==',
  'nonce': 'bm9uY2U=',
  'reqNonce': 'AQIDBA==',
  'sig': 'c2ln',
  ...overrides,
};

void main() {
  group('reading a v2 request', () {
    test('round-trips every field the transcript binds', () {
      final parsed = parseRequestBody(body(requestJson()))!;
      expect(parsed.id, requester);
      expect(parsed.epoch, 4);
      expect(parsed.nonce, 'AQIDBA==');
      expect(parsed.sig, 'c2ln');
      expect(parsed.toJson(), requestJson());
    });

    // The forgery this closes: a member minting a request in another member's name, so a co-member
    // seals a whole conversation to a key derived for a device that never asked.
    test('REFUSES an unsigned v1 request — there is no downgrade path', () {
      expect(parseRequestBody(body({'id': requester, 'epoch': 4})), isNull);
    });

    test('refuses a v2 body with the signature stripped out', () {
      expect(parseRequestBody(body(requestJson({'sig': ''}))), isNull);
    });

    test('refuses a future version rather than reading it as v2', () {
      expect(parseRequestBody(body(requestJson({'v': 3}))), isNull);
    });

    test('refuses an identity that is not a resolvable credential', () {
      // A legacy `user:device` leaf: there is no user to compare against the envelope's
      // authenticated poster, and no leaf in the ratchet tree to verify a signature against.
      expect(parseRequestBody(body(requestJson({'id': 'bob:dev-b'}))), isNull);
    });

    test('refuses a body that is not JSON at all', () {
      expect(
        parseRequestBody(Uint8List.fromList(utf8.encode('{ not'))),
        isNull,
      );
    });
  });

  group('reading a v2 offer', () {
    test('round-trips every field the transcript binds', () {
      final parsed = parseOfferBody(body(offerJson()))!;
      expect(parsed.from, offerer);
      expect(parsed.to, requester);
      expect(parsed.historyId, 'hist-1');
      expect(parsed.reqNonce, 'AQIDBA==');
      expect(parsed.toJson(), offerJson());
    });

    test('REFUSES an unsigned v1 offer', () {
      expect(
        parseOfferBody(
          body({
            'to': requester,
            'epoch': 4,
            'historyId': 'hist-1',
            'salt': 'c2FsdA==',
            'nonce': 'bm9uY2U=',
          }),
        ),
        isNull,
      );
    });

    // Without `from` there is no leaf key to verify against, so the signature could not be checked
    // at all — which is the same as not having one.
    test('refuses an offer that names no offerer', () {
      expect(parseOfferBody(body(offerJson({'from': ''}))), isNull);
    });

    // An offer that quotes no request nonce is a free-floating blob anyone may push at a device.
    test('refuses an offer that answers no request', () {
      expect(parseOfferBody(body(offerJson({'reqNonce': ''}))), isNull);
    });

    test('is null on a non-string field rather than coercing it', () {
      expect(parseOfferBody(body(offerJson({'to': 42}))), isNull);
    });
  });

  group('the server-authenticated poster', () {
    // The server is untrusted for message CONTENT, but it does authenticate the session that posted
    // a control message. That makes the envelope a second, independent witness: an insider forging
    // a body in somebody else's name has to post it from that person's account as well.
    test('accepts a claim only when it matches the poster', () {
      expect(posterMatchesClaim(requester, 'bob'), isTrue);
      expect(posterMatchesClaim(requester, 'alice'), isFalse);
    });

    group('history provider account', () {
      test('accepts another device of the same canonical account', () {
        expect(
          sameAccountIdentities(
            'mimi://test.example/d/bob/phone',
            'mimi://test.example/d/bob/laptop',
          ),
          isTrue,
        );
      });

      test('refuses another participant with a valid credential', () {
        expect(sameAccountIdentities(requester, offerer), isFalse);
      });

      test('refuses the same bare user on another host', () {
        expect(
          sameAccountIdentities(
            'mimi://test.example/d/bob/phone',
            'mimi://other.example/d/bob/laptop',
          ),
          isFalse,
        );
      });
    });

    test('does not fail when there is no poster to compare against', () {
      // An older server sends none. The MLS signature is still the check that must hold.
      expect(posterMatchesClaim(requester, ''), isTrue);
    });

    test('refuses a claim that resolves to no user at all', () {
      expect(posterMatchesClaim('not-a-credential', 'bob'), isFalse);
    });
  });

  group('the cross-platform golden vectors', () {
    // Resolved from the mobile package root, which is where `flutter test` runs.
    final file = File('../test/fixtures/mls_history_vectors.json');
    final vectors = jsonDecode(file.readAsStringSync()) as Map<String, dynamic>;

    test('this client speaks the version the vectors pin', () {
      expect(historyVersion, vectors['version']);
    });

    test('a request body matches the shared vector byte for byte', () {
      final v = vectors['request'] as Map<String, dynamic>;
      final expected = Map<String, dynamic>.from(v['body'] as Map);
      final built = HistoryRequestBody(
        id: v['requester'] as String,
        epoch: v['epoch'] as int,
        nonce: v['nonceBase64'] as String,
        sig: expected['sig'] as String,
      ).toJson();
      expect(
        built,
        expected,
        reason:
            'the request body no longer matches the cross-platform vector; a web client '
            'would build a different transcript from it and every signature would fail',
      );
      // And it reads back: the encoder and the parser agree, which is what a round trip between
      // two clients actually is.
      expect(parseRequestBody(body(built))!.toJson(), expected);
    });

    test('an offer body matches the shared vector byte for byte', () {
      final v = vectors['offer'] as Map<String, dynamic>;
      final request = vectors['request'] as Map<String, dynamic>;
      final expected = Map<String, dynamic>.from(v['body'] as Map);
      final built = HistoryOfferBody(
        from: v['offerer'] as String,
        to: v['requester'] as String,
        epoch: v['epoch'] as int,
        historyId: v['historyId'] as String,
        salt: base64Encode(_unhex(v['saltHex'] as String)),
        nonce: base64Encode(_unhex(v['nonceHex'] as String)),
        reqNonce: request['nonceBase64'] as String,
        sig: expected['sig'] as String,
      ).toJson();
      expect(
        built,
        expected,
        reason:
            'the offer body no longer matches the cross-platform vector; a web client would '
            'build a different transcript from it and every signature would fail',
      );
      expect(parseOfferBody(body(built))!.toJson(), expected);
    });
  });
}

List<int> _unhex(String hex) => [
  for (var i = 0; i < hex.length; i += 2)
    int.parse(hex.substring(i, i + 2), radix: 16),
];
