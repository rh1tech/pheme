// Encrypting a photo, and getting it back. A port of web/src/lib/photo.ts.
//
// The construction, and the reason for each part:
//
//   * a FRESH 32-byte key per photo, never reused. Two photos under one key is one nonce collision
//     away from leaking both, and there is no reason to economise on 32 bytes.
//   * a FRESH 12-byte random nonce, PREPENDED to the ciphertext. Prepending means the caller stores
//     one opaque blob and can never get the two out of step — a nonce is not a secret, it just must
//     never repeat.
//   * AES-256-GCM, with the blob's purpose bound in as additional data, so a blob cannot be passed
//     off as something else sealed under the same key.
//
// The sealed bytes go to the server, which stores them as application/octet-stream and cannot open
// them. The KEY goes inside the MLS-encrypted message that references the photo — so the server holds
// the lock and never receives the key, and the two never meet anywhere it can reach.
//
// Pure Dart, like call_envelope.dart and for the same reason: this format has to match a browser byte
// for byte, and the only thing that proves it does is a golden vector generated from the web — which
// has to be able to run on a CI host with no NDK, or in practice it will not run at all.

import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';

/// Bound in as additional data. A photo blob cannot be passed off as anything else sealed under the
/// same key.
final _aad = Uint8List.fromList(utf8.encode('pheme.photo.v1'));

const _nonceBytes = 12;
const _keyBytes = 32;
const _tagBytes = 16;

final _aes = AesGcm.with256bits();

/// A photo sealed and ready to upload.
class SealedPhoto {
  const SealedPhoto({
    required this.bytes,
    required this.key,
    required this.width,
    required this.height,
    required this.mime,
    required this.size,
  });

  /// nonce ‖ ciphertext ‖ tag. What the server stores, and all it ever sees.
  final Uint8List bytes;

  /// base64 of the raw key. Goes inside the encrypted message, never to the server.
  final String key;

  final int width;
  final int height;
  final String mime;

  /// Size of the PLAINTEXT, for the UI.
  final int size;
}

/// A fresh key. Never derived, never reused — a photo has nothing to do with any other photo.
Uint8List newPhotoKey() => _randomBytes(_keyBytes);

Uint8List _randomBytes(int n) {
  final random = Random.secure();
  return Uint8List.fromList(List.generate(n, (_) => random.nextInt(256)));
}

/// Seals already-encoded image bytes, returning `nonce ‖ ciphertext ‖ tag`.
///
/// The nonce is a parameter so a golden vector can pin the output. Everywhere else it is random, and
/// [sealPhoto] is what callers should use.
Future<Uint8List> sealPhotoBytes({
  required Uint8List key,
  required Uint8List nonce,
  required Uint8List plaintext,
}) async {
  final box = await _aes.encrypt(
    plaintext,
    secretKey: SecretKey(key),
    nonce: nonce,
    aad: _aad,
  );
  // WebCrypto appends the tag to the ciphertext; `cryptography` keeps them apart. The wire format is
  // the browser's, so put it back.
  return Uint8List.fromList([...nonce, ...box.cipherText, ...box.mac.bytes]);
}

/// Seals a photo under a fresh key and nonce.
Future<({Uint8List bytes, Uint8List key})> sealPhoto(
  Uint8List plaintext,
) async {
  final key = newPhotoKey();
  final bytes = await sealPhotoBytes(
    key: key,
    nonce: _randomBytes(_nonceBytes),
    plaintext: plaintext,
  );
  return (bytes: bytes, key: key);
}

/// Opens a sealed photo.
///
/// Throws when the key is wrong or the bytes were tampered with. There is no lenient path: a photo
/// that does not open is not the photo that was sent, and showing something else would be worse than
/// showing nothing.
Future<Uint8List> openPhoto({
  required String keyBase64,
  required Uint8List sealed,
}) async {
  if (sealed.length <= _nonceBytes + _tagBytes) {
    throw const FormatException('photo is truncated');
  }

  final nonce = sealed.sublist(0, _nonceBytes);
  final rest = sealed.sublist(_nonceBytes);
  final split = rest.length - _tagBytes;

  final box = SecretBox(
    rest.sublist(0, split),
    nonce: nonce,
    mac: Mac(rest.sublist(split)),
  );

  final opened = await _aes.decrypt(
    box,
    secretKey: SecretKey(base64Decode(keyBase64)),
    aad: _aad,
  );
  return Uint8List.fromList(opened);
}
