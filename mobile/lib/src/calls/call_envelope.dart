// The wire format of a call signal, and the only place call signalling is encrypted.
//
// A port of web/src/lib/callEnvelope.ts. Every byte here has to match that file, because a phone and a
// browser have to be able to call each other — and when they cannot, the failure is silent: the
// ciphertext simply does not open, and neither end has anything to log except "the call did not
// connect".
//
// A signal is an envelope: a small cleartext header, and a sealed body.
//
//   { h: {...}, n: "<nonce>", c: "<ciphertext>" }
//
// The HEADER is readable by the server, and has to be. It says which call this is, which epoch the key
// was derived at, which device sent it, and in what order — all things a receiver needs before it can
// decrypt anything, and none of which tell the server what is being said. It is also bound into the
// ciphertext as additional authenticated data, so a server that rewrote any of it would make the body
// fail to open rather than quietly redirect the call.
//
// The BODY is what matters: the SDP. It carries the DTLS fingerprint that WebRTC's own media
// encryption is authenticated against, so a server able to rewrite it could substitute its own
// fingerprint and sit in the middle of the call, listening to all of it. Sealing it under a key derived
// from the conversation's MLS group — which the server does not have — is what makes that impossible,
// and it is the whole reason this file exists.

// AES-GCM comes from the pure-Dart `cryptography` package here, and NOT from the Rust facade that
// does everything else. That looks inconsistent and is deliberate.
//
// This is the one piece of crypto in the app with a cross-client interop requirement: these exact
// bytes have to match what a browser produces, or a phone cannot call a laptop. The only thing that
// proves they match is a golden vector generated from the web client and asserted here — and a test
// that needs a compiled native library cannot run on a CI host, or on a laptop with no NDK, which
// means in practice it would not run. A pure-Dart implementation makes that test run everywhere, on
// every commit. Sharing an implementation would have been one way to be sure the two agree; proving
// it on every build is a better one.

import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';

/// What a signal is. Only `epoch-mismatch` is ever sent in the clear.
enum CallKind {
  invite,
  answer,
  decline,
  busy,
  hangup;

  static CallKind? fromWire(String? v) => switch (v) {
    'invite' => CallKind.invite,
    'answer' => CallKind.answer,
    'decline' => CallKind.decline,
    'busy' => CallKind.busy,
    'hangup' => CallKind.hangup,
    _ => null,
  };

  String get wire => name;
}

/// The one cleartext control there is.
const epochMismatch = 'epoch-mismatch';

/// The cleartext part. The server sees exactly this and no more.
class CallHeader {
  const CallHeader({
    required this.callId,
    required this.epoch,
    required this.from,
    required this.seq,
    this.control,
  });

  static const version = 1;

  final String callId;

  /// The MLS epoch the sender derived its key at. In the clear, because of a bootstrapping problem —
  /// see [control].
  final int epoch;

  /// The sending DEVICE, as `userId:deviceId`. The receiver derives its key from this.
  final String from;

  /// Monotonic per (callId, from). A receiver rejects anything it has already seen.
  final int seq;

  /// An unencrypted control, or null for a normal sealed signal.
  ///
  /// There is exactly one, and it exists because of a bootstrapping problem: a device at a LATER epoch
  /// than the sender cannot derive the sender's key at all — MLS's exporter only exports from the
  /// current epoch — so it cannot reply in a sealed envelope to say so. `epoch-mismatch` is that reply.
  /// It carries no secret and asserts nothing: it says "I am at epoch N", and the caller re-derives and
  /// tries again.
  final String? control;

  Map<String, dynamic> toJson() => {
    'v': version,
    'callId': callId,
    'epoch': epoch,
    'from': from,
    'seq': seq,
    if (control != null) 'control': control,
  };

  static CallHeader? fromJson(Map<String, dynamic> j) {
    if (j['v'] != version) return null;
    final callId = j['callId'];
    final from = j['from'];
    final seq = j['seq'];
    if (callId is! String || from is! String || seq is! num) return null;

    return CallHeader(
      callId: callId,
      epoch: (j['epoch'] as num?)?.toInt() ?? 0,
      from: from,
      seq: seq.toInt(),
      control: j['control'] as String?,
    );
  }
}

/// The sealed part: what the two people are actually saying to each other.
class CallBody {
  const CallBody({required this.kind, this.sdp});

  final CallKind kind;

  /// The SDP offer or answer. Absent on hangup, decline and busy.
  final String? sdp;

  Map<String, dynamic> toJson() => {
    'kind': kind.wire,
    if (sdp != null) 'sdp': sdp,
  };
}

/// The bytes bound into the ciphertext as additional authenticated data.
///
/// Serialised as a FIXED-ORDER ARRAY, not as the header object. AES-GCM compares these byte for byte,
/// and two JSON encoders that order an object's keys differently would produce two different AADs — so
/// every signal between a phone and a browser would fail to open, with no error anywhere that says why.
/// The array removes the possibility rather than trusting two languages to agree about key order.
///
/// A missing control serialises as the empty string, not as null, because that is what the web does.
Uint8List headerAad(CallHeader h) {
  final canonical = jsonEncode([
    CallHeader.version,
    h.callId,
    h.epoch,
    h.from,
    h.seq,
    h.control ?? '',
  ]);
  return Uint8List.fromList(utf8.encode(canonical));
}

/// AES-256-GCM. The web uses WebCrypto's AES-GCM with a 128-bit tag, which is this.
final _aes = AesGcm.with256bits();

/// 12 random bytes, freshly generated for every signal. NEVER a counter.
///
/// Every device in the conversation can derive every other device's call key — that is how they read
/// each other's signals — so two devices counting from zero would eventually seal different messages
/// under the same key and nonce. An AES-GCM nonce collision does not merely leak those two plaintexts:
/// it leaks the authentication key, and lets an attacker forge signals at will. Random removes the
/// possibility; a counter would depend on nobody ever restarting one.
Uint8List newNonce() {
  final random = Random.secure();
  return Uint8List.fromList(List.generate(12, (_) => random.nextInt(256)));
}

/// Seals a body under a call key. Split out so the golden vector can pin it with a fixed nonce.
Future<Uint8List> sealBody({
  required Uint8List key,
  required Uint8List nonce,
  required Uint8List aad,
  required Uint8List plaintext,
}) async {
  final box = await _aes.encrypt(
    plaintext,
    secretKey: SecretKey(key),
    nonce: nonce,
    aad: aad,
  );
  // WebCrypto appends the 16-byte tag to the ciphertext; `cryptography` keeps them apart. The wire
  // format is the browser's, so put it back.
  return Uint8List.fromList([...box.cipherText, ...box.mac.bytes]);
}

/// Opens a body sealed under a call key.
///
/// Throws when the key is wrong, or when the header was tampered with — it is bound in as [aad], so a
/// server that rewrote any of it would make this fail rather than quietly redirect the call.
Future<Uint8List> openBody({
  required Uint8List key,
  required Uint8List nonce,
  required Uint8List aad,
  required Uint8List ciphertext,
}) async {
  const tagLength = 16;
  if (ciphertext.length < tagLength) {
    throw const FormatException('signal is too short to be sealed');
  }

  final split = ciphertext.length - tagLength;
  final box = SecretBox(
    ciphertext.sublist(0, split),
    nonce: nonce,
    mac: Mac(ciphertext.sublist(split)),
  );

  final opened = await _aes.decrypt(box, secretKey: SecretKey(key), aad: aad);
  return Uint8List.fromList(opened);
}

/// Seals a signal under this device's call key, returning the bytes to post.
///
/// The result is base64(JSON(envelope)) — and the transport then base64s that again, because the
/// server's ciphertext field is a Go []byte. Two layers, and unwrapping one too few is the easiest
/// mistake to make here.
Future<Uint8List> sealSignal(
  Uint8List secret,
  CallHeader header,
  CallBody body,
) async {
  final nonce = newNonce();
  final sealed = await sealBody(
    key: secret,
    nonce: nonce,
    aad: headerAad(header),
    plaintext: Uint8List.fromList(utf8.encode(jsonEncode(body.toJson()))),
  );

  return _encodeEnvelope({
    'h': header.toJson(),
    'n': base64Encode(nonce),
    'c': base64Encode(sealed),
  });
}

/// A cleartext control. The only one is `epoch-mismatch`, and it carries nothing secret.
Uint8List sealControl(CallHeader header) =>
    _encodeEnvelope({'h': header.toJson()});

/// Reads the cleartext header. Null for anything that is not a v1 envelope.
CallHeader? openHeader(Uint8List wire) {
  try {
    final envelope = jsonDecode(utf8.decode(wire)) as Map<String, dynamic>;
    final h = envelope['h'];
    if (h is! Map) return null;
    return CallHeader.fromJson(h.cast<String, dynamic>());
  } on Object {
    return null;
  }
}

/// Opens a sealed signal with the SENDER's key.
///
/// Throws if the header was tampered with (it is bound in as additional data), if the key is wrong —
/// which is what a server substituting its own signal would look like — or if the body is not a signal.
/// There is no lenient path: a signal that does not open is not a signal.
Future<CallBody> openSignal(Uint8List secret, Uint8List wire) async {
  final envelope = jsonDecode(utf8.decode(wire)) as Map<String, dynamic>;

  final n = envelope['n'];
  final c = envelope['c'];
  if (n is! String || c is! String) {
    throw const FormatException('signal is not sealed');
  }

  final header = CallHeader.fromJson(
    (envelope['h'] as Map).cast<String, dynamic>(),
  );
  if (header == null) throw const FormatException('not a call signal');

  final opened = await openBody(
    key: secret,
    nonce: base64Decode(n),
    aad: headerAad(header),
    ciphertext: base64Decode(c),
  );

  final body = jsonDecode(utf8.decode(opened)) as Map<String, dynamic>;
  final kind = CallKind.fromWire(body['kind'] as String?);
  if (kind == null) throw const FormatException('signal has no kind');

  return CallBody(kind: kind, sdp: body['sdp'] as String?);
}

Uint8List _encodeEnvelope(Map<String, dynamic> envelope) =>
    Uint8List.fromList(utf8.encode(jsonEncode(envelope)));
