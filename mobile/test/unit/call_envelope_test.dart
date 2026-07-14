// The golden vector for call signalling.
//
// AES-GCM compares additional authenticated data BYTE FOR BYTE. If Dart's jsonEncode and JavaScript's
// JSON.stringify ever disagree about how to serialise the header — a space, a key order, how an
// integer or an empty string is written — then every call between a phone and a browser fails to
// connect, and NOTHING anywhere says why. The ciphertext simply does not open. There is no error to
// log, no field to inspect, and both ends are behaving exactly as designed.
//
// The vector below was generated from the WEB implementation (web/src/lib/callEnvelope.ts, run through
// WebCrypto), not from this one. That direction matters: it makes this test an assertion that Dart
// agrees with the browser, rather than an assertion that Dart agrees with itself.
//
// The same vector is asserted on the web side in web/src/lib/callEnvelope.test.ts. If either moves,
// both fail — which is the point.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/calls/call_envelope.dart';

/// 00 01 02 … 1f
final _key = Uint8List.fromList(List.generate(32, (i) => i));

/// a0 a1 a2 … ab. Fixed here ONLY so the vector is reproducible. A real signal uses 12 random bytes,
/// every time — see newNonce().
final _nonce = Uint8List.fromList(List.generate(12, (i) => 0xa0 + i));

const _header = CallHeader(
  callId: 'b0a1c2d3-e4f5-4607-8899-aabbccddeeff',
  epoch: 7,
  from: 'user-1:device-1',
  seq: 3,
);

const _bodyJson =
    '{"kind":"invite","sdp":"v=0\\r\\no=- 1 1 IN IP4 127.0.0.1\\r\\n"}';

/// What web/src/lib/callEnvelope.ts produces for the header above.
const _aadCanonical =
    '[1,"b0a1c2d3-e4f5-4607-8899-aabbccddeeff",7,"user-1:device-1",3,""]';

/// What WebCrypto produces sealing [_bodyJson] under [_key] and [_nonce] with that AAD.
const _ciphertextHex =
    '9d3a17442baf2085400ce9a56e0ea5fc5c8e2a74e295784eea3316da0df71b6e'
    'ef5b67ce8f13737411bc4d983d5ab2cb7035766652fe2b223302657cd940f428'
    '76b49e9b4544b53ef73e3fed30';

String _hex(List<int> bytes) =>
    bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

void main() {
  group('call envelope golden vector (generated from the web client)', () {
    test(
      'the AAD is the fixed-order array the web produces, byte for byte',
      () {
        expect(utf8.decode(headerAad(_header)), _aadCanonical);
      },
    );

    test('an absent control serialises as "" and not as null', () {
      // The web writes `h.control ?? ''`. A null here would change the AAD and break every signal.
      expect(_aadCanonical.endsWith(',""]'), isTrue);
      expect(utf8.decode(headerAad(_header)).contains('null'), isFalse);
    });

    test('the body serialises exactly as the web serialises it', () {
      const body = CallBody(
        kind: CallKind.invite,
        sdp: 'v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n',
      );
      expect(jsonEncode(body.toJson()), _bodyJson);
    });

    // The one that actually proves it. Everything above could pass while this fails.
    test('sealing reproduces the ciphertext WebCrypto produced', () async {
      final sealed = await sealBody(
        key: _key,
        nonce: _nonce,
        aad: headerAad(_header),
        plaintext: Uint8List.fromList(utf8.encode(_bodyJson)),
      );
      expect(_hex(sealed), _ciphertextHex);
    });

    test('a signal sealed by the web opens here', () async {
      final opened = await openBody(
        key: _key,
        nonce: _nonce,
        aad: headerAad(_header),
        ciphertext: Uint8List.fromList(_unhex(_ciphertextHex)),
      );
      expect(utf8.decode(opened), _bodyJson);
    });

    test('a tampered header makes the body fail to open', () async {
      // A server that rewrote the epoch — to force a downgrade to a key it had seen — would have to
      // make this succeed. It cannot: the header is bound in as additional data.
      const tampered = CallHeader(
        callId: 'b0a1c2d3-e4f5-4607-8899-aabbccddeeff',
        epoch: 6, // was 7
        from: 'user-1:device-1',
        seq: 3,
      );

      expect(
        () => openBody(
          key: _key,
          nonce: _nonce,
          aad: headerAad(tampered),
          ciphertext: Uint8List.fromList(_unhex(_ciphertextHex)),
        ),
        throwsA(anything),
      );
    });
  });

  group('nonce', () {
    test('is 12 bytes and never repeats', () {
      // Not a counter, and this is not a style preference: every device in the conversation can derive
      // every other device's call key, so two devices counting from zero would collide — and an
      // AES-GCM nonce collision leaks the authentication key itself, not merely the two plaintexts.
      final seen = <String>{};
      for (var i = 0; i < 200; i++) {
        final nonce = newNonce();
        expect(nonce, hasLength(12));
        expect(seen.add(_hex(nonce)), isTrue, reason: 'nonce repeated');
      }
    });
  });
}

List<int> _unhex(String hex) => [
  for (var i = 0; i < hex.length; i += 2)
    int.parse(hex.substring(i, i + 2), radix: 16),
];
