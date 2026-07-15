// The cross-client contract for message content and photo encryption.
//
// Both are formats a browser also produces, and both fail SILENTLY when the two disagree: a message
// arrives blank, or a photo will not decode, and nothing anywhere says why — both clients are doing
// exactly what they were told. So both are pinned here against vectors generated from the web
// implementation, and the same vectors are asserted on the web side.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/chat_content.dart';
import 'package:pheme_mobile/src/crypto/photo_crypto.dart';

String _hex(List<int> bytes) =>
    bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();

List<int> _unhex(String hex) => [
  for (var i = 0; i < hex.length; i += 2)
    int.parse(hex.substring(i, i + 2), radix: 16),
];

void main() {
  group('ChatPhoto equality', () {
    // The photo cache (photoProvider, keyed by the ChatPhoto) relies on value equality: two photos
    // with the same fields must be equal and share a hash, or a re-parsed message re-downloads every
    // photo and the feed blinks.
    ChatPhoto photo({String id = 'blob-1', int width = 100}) => ChatPhoto(
      id: id,
      key: 'a2V5',
      width: width,
      height: 50,
      mime: 'image/webp',
      size: 1234,
    );

    test('two photos with identical fields are equal and share a hash', () {
      expect(photo(), photo());
      expect(photo().hashCode, photo().hashCode);
    });

    test('a differing field breaks equality', () {
      expect(photo(), isNot(photo(id: 'blob-2')));
      expect(photo(), isNot(photo(width: 200)));
    });
  });

  group('content codec', () {
    test('a plain message serialises exactly as the web serialises it', () {
      const content = ChatContent(body: 'hello');
      expect(utf8.decode(serializeContent(content)), '{"body":"hello"}');
    });

    // Absent fields are OMITTED, not written as null. The web omits them, and a null where nothing is
    // expected is a difference for no reason — but it is a difference, and AES-GCM does not care about
    // intent.
    test('absent fields are omitted rather than written as null', () {
      final json = utf8.decode(serializeContent(const ChatContent(body: 'hi')));
      expect(json.contains('null'), isFalse);
      expect(json.contains('replyTo'), isFalse);
      expect(json.contains('photos'), isFalse);
    });

    test('a reply carries only the id it is replying to', () {
      const content = ChatContent(body: 'agreed', replyTo: 'msg-1');
      expect(
        utf8.decode(serializeContent(content)),
        '{"body":"agreed","replyTo":"msg-1"}',
      );
    });

    test('a photo carries its key, and the key never leaves the message', () {
      const content = ChatContent(
        body: 'look',
        photos: [
          ChatPhoto(
            id: 'blob-1',
            key: 'a2V5',
            width: 1200,
            height: 800,
            mime: 'image/jpeg',
            size: 4096,
          ),
        ],
      );

      expect(
        utf8.decode(serializeContent(content)),
        '{"body":"look","photos":[{"id":"blob-1","key":"a2V5","w":1200,'
        '"h":800,"mime":"image/jpeg","size":4096}]}',
      );
    });

    test('round-trips', () {
      const content = ChatContent(
        body: 'look',
        replyTo: 'msg-1',
        photos: [
          ChatPhoto(
            id: 'blob-1',
            key: 'a2V5',
            width: 12,
            height: 8,
            mime: 'image/jpeg',
            size: 9,
          ),
        ],
      );

      final read = parseContent(serializeContent(content));
      expect(read.body, 'look');
      expect(read.replyTo, 'msg-1');
      expect(read.photos.single.id, 'blob-1');
      expect(read.photos.single.key, 'a2V5');
      expect(read.photos.single.aspectRatio, closeTo(1.5, 0.001));
    });

    // An older client must still read a message from a newer one. Showing what we understand and
    // ignoring the rest is the difference between "a photo I cannot see yet" and "a blank bubble".
    test('a message from a newer client still shows what we understand', () {
      final future = utf8.encode(
        '{"body":"hi","replyTo":"m1","reactions":["🎉"],'
        '"video":{"id":"v1"},"photos":[]}',
      );

      final read = parseContent(Uint8List.fromList(future));
      expect(read.body, 'hi');
      expect(read.replyTo, 'm1');
    });

    // One broken photo must not cost the caption and the good ones beside it.
    test('a malformed photo is dropped, not fatal', () {
      final mixed = utf8.encode(
        '{"body":"two of three","photos":['
        '{"id":"a","key":"k","w":1,"h":1,"mime":"image/jpeg","size":1},'
        '{"id":"","key":"k","mime":"image/jpeg"},'
        '{"key":"k","mime":"image/jpeg"},'
        '{"id":"c","key":"k","w":1,"h":1,"mime":"image/jpeg","size":1}]}',
      );

      final read = parseContent(Uint8List.fromList(mixed));
      expect(read.body, 'two of three');
      expect(read.photos.map((p) => p.id), ['a', 'c']);
    });

    test('garbage decodes to an empty body rather than throwing', () {
      expect(
        parseContent(Uint8List.fromList(utf8.encode('not json'))).body,
        '',
      );
      expect(parseContent(Uint8List.fromList(utf8.encode('[1,2,3]'))).body, '');
    });
  });

  group('photo encryption golden vector (generated from the web client)', () {
    /// 40 41 42 … 5f
    final key = Uint8List.fromList(List.generate(32, (i) => 0x40 + i));

    /// c0 c1 c2 … cb. Fixed ONLY so the vector reproduces. A real photo gets 12 random bytes.
    final nonce = Uint8List.fromList(List.generate(12, (i) => 0xc0 + i));

    const plaintext = 'a tiny pretend jpeg';
    const keyB64 = 'QEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaW1xdXl8=';

    /// nonce ‖ ciphertext ‖ tag, as WebCrypto produces it.
    const sealedHex =
        'c0c1c2c3c4c5c6c7c8c9cacb3e6444a714e31d9b06dc5053c5c782a7ef903998'
        'cba5a38a447d297e638a7f9282561b';

    test('the key encodes as the web encodes it', () {
      expect(base64Encode(key), keyB64);
    });

    // The one that actually proves it. A photo sealed on a phone has to open in a browser.
    test('sealing reproduces the bytes WebCrypto produced', () async {
      final sealed = await sealPhotoBytes(
        key: key,
        nonce: nonce,
        plaintext: Uint8List.fromList(utf8.encode(plaintext)),
      );
      expect(_hex(sealed), sealedHex);
    });

    test('a photo sealed by the web opens here', () async {
      final opened = await openPhoto(
        keyBase64: keyB64,
        sealed: Uint8List.fromList(_unhex(sealedHex)),
      );
      expect(utf8.decode(opened), plaintext);
    });

    test('the nonce is prepended, so a blob is one opaque thing', () async {
      final sealed = await sealPhotoBytes(
        key: key,
        nonce: nonce,
        plaintext: Uint8List.fromList(utf8.encode(plaintext)),
      );
      expect(sealed.sublist(0, 12), nonce);
    });

    test('a photo does not open under the wrong key', () async {
      final wrong = base64Encode(Uint8List.fromList(List.filled(32, 0)));
      expect(
        () => openPhoto(
          keyBase64: wrong,
          sealed: Uint8List.fromList(_unhex(sealedHex)),
        ),
        throwsA(anything),
      );
    });

    test('a tampered photo does not open', () async {
      // The AAD binds the blob's purpose, and the tag covers every byte. Flip one and it is gone.
      final tampered = _unhex(sealedHex);
      tampered[20] ^= 0x01;

      expect(
        () =>
            openPhoto(keyBase64: keyB64, sealed: Uint8List.fromList(tampered)),
        throwsA(anything),
      );
    });

    test('every photo gets a fresh key — two photos never share one', () {
      final seen = <String>{};
      for (var i = 0; i < 100; i++) {
        expect(seen.add(_hex(newPhotoKey())), isTrue, reason: 'key repeated');
      }
    });
  });
}
