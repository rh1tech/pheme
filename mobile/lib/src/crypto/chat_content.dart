// What actually gets encrypted: the innermost payload of a chat message.
//
// A separate layer from the message envelope on purpose. The server sees the envelope — who sent it,
// when, what content type — and it must never be able to see this. Keeping the two apart in the code
// is what keeps a field from drifting across the line by accident.
//
// ------------------------------------------------------------------------------------------------
// THIS SHAPE IS A CROSS-CLIENT CONTRACT. The web has the same codec in web/src/lib/chatContent.ts and
// the two have to agree — a field one writes and the other cannot read is a message that arrives
// blank on somebody's screen.
//
// It is also why every field but `body` is OPTIONAL, and why parsing tolerates the unfamiliar: a
// client that has not been updated must still read a message from one that has, showing what it
// understands and quietly ignoring the rest. A photo from a newer client renders as its caption on an
// older one. It does not render as nothing.
// ------------------------------------------------------------------------------------------------

import 'dart:convert';
import 'dart:typed_data';

/// One photo, as it appears inside an encrypted message.
///
/// THE KEY IS HERE, AND THAT IS THE WHOLE DESIGN. The photo is AES-GCM ciphertext in the server's
/// blob store; the key that opens it exists only inside this message, which is itself end-to-end
/// encrypted. The server holds a blob it cannot open and never receives the key, and the two never
/// meet anywhere it can reach.
///
/// The dimensions and the mime type are here for the same reason: they are properties of the
/// PLAINTEXT. The server stores the ciphertext as application/octet-stream and learns nothing but a
/// size.
class ChatPhoto {
  const ChatPhoto({
    required this.id,
    required this.key,
    required this.width,
    required this.height,
    required this.mime,
    required this.size,
  });

  /// The blob id, from POST /v1/conversations/{id}/attachments.
  final String id;

  /// base64 AES-256-GCM key, 32 bytes. Fresh for every photo — never reused.
  final String key;

  /// Pixel dimensions, so a bubble can reserve the right space before the bytes arrive — otherwise
  /// the whole feed jumps when a photo finally decodes.
  final int width;
  final int height;

  final String mime;

  /// Size of the plaintext.
  final int size;

  double get aspectRatio => height > 0 ? width / height : 1;

  // Value equality, keyed on the fields that identify the blob. photoProvider is a
  // FutureProvider.family keyed by the photo, and parsing a message's content creates a
  // fresh ChatPhoto each time; without this, an equal photo from a re-parse (reopening a
  // chat, a re-decrypt) is a different cache key, so every photo re-downloads and blinks.
  @override
  bool operator ==(Object other) =>
      other is ChatPhoto &&
      other.id == id &&
      other.key == key &&
      other.width == width &&
      other.height == height &&
      other.mime == mime &&
      other.size == size;

  @override
  int get hashCode => Object.hash(id, key, width, height, mime, size);

  Map<String, dynamic> toJson() => {
    'id': id,
    'key': key,
    'w': width,
    'h': height,
    'mime': mime,
    'size': size,
  };

  /// Null for an entry we cannot make sense of — see [parseContent], which drops those rather than
  /// failing the whole message.
  static ChatPhoto? fromJson(Map<String, dynamic> j) {
    final id = j['id'];
    final key = j['key'];
    final mime = j['mime'];
    if (id is! String || id.isEmpty) return null;
    if (key is! String || key.isEmpty) return null;
    if (mime is! String || mime.isEmpty) return null;

    return ChatPhoto(
      id: id,
      key: key,
      width: (j['w'] as num?)?.toInt() ?? 0,
      height: (j['h'] as num?)?.toInt() ?? 0,
      mime: mime,
      size: (j['size'] as num?)?.toInt() ?? 0,
    );
  }
}

/// The body of a chat message, before encryption.
class ChatContent {
  const ChatContent({required this.body, this.replyTo, this.photos = const []});

  final String body;

  /// The message this one replies to.
  ///
  /// Just an id. The quoted text is NOT copied in, deliberately: a client renders the quote from the
  /// message it already holds, and if it does not hold it — a device that joined after the quoted
  /// message was sent, and so can never decrypt it — it says so rather than showing a quote it cannot
  /// verify. Copying the text in would be a lie waiting to happen: the sender could quote you as
  /// saying anything at all, and the recipient would have no way to check.
  final String? replyTo;

  final List<ChatPhoto> photos;

  bool get hasPhotos => photos.isNotEmpty;
}

/// Serialises content for encryption.
///
/// The exact bytes the web produces, because both ends have to agree or every cross-client message is
/// gibberish. Absent fields are OMITTED rather than written as null — the web omits them, and a null
/// where nothing is expected is a difference for no reason.
Uint8List serializeContent(ChatContent content) {
  final json = <String, dynamic>{'body': content.body};

  final replyTo = content.replyTo;
  if (replyTo != null && replyTo.isNotEmpty) json['replyTo'] = replyTo;
  if (content.photos.isNotEmpty) {
    json['photos'] = content.photos.map((p) => p.toJson()).toList();
  }

  return Uint8List.fromList(utf8.encode(jsonEncode(json)));
}

/// Reads decrypted plaintext back into content.
///
/// Tolerant: a message that decrypted correctly but does not fully parse is a message from a newer
/// client, not a broken one, and showing what we understand beats throwing away the lot. A single
/// malformed photo does not cost the caption and the three good ones alongside it.
ChatContent parseContent(Uint8List plaintext) {
  try {
    final json = jsonDecode(utf8.decode(plaintext));
    if (json is! Map) return const ChatContent(body: '');

    final body = json['body'];
    if (body is! String) return const ChatContent(body: '');

    final replyTo = json['replyTo'];
    final photos = <ChatPhoto>[];

    final rawPhotos = json['photos'];
    if (rawPhotos is List) {
      for (final entry in rawPhotos) {
        if (entry is! Map) continue;
        final photo = ChatPhoto.fromJson(entry.cast<String, dynamic>());
        if (photo != null) photos.add(photo);
      }
    }

    return ChatContent(
      body: body,
      replyTo: replyTo is String && replyTo.isNotEmpty ? replyTo : null,
      photos: photos,
    );
  } on FormatException {
    return const ChatContent(body: '');
  }
}
