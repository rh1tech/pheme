// What actually gets encrypted: the innermost payload of a chat message.
//
// A separate layer from the message envelope on purpose. The server sees the envelope — who sent
// it, when, what content type — and it must never be able to see this. Keeping the two apart in the
// code is what keeps a field from drifting across the line by accident.

import 'dart:convert';
import 'dart:typed_data';

/// The body of a chat message, before encryption.
///
/// Just a body for now. Attachments are not implemented on any client, and a field here that the
/// web cannot read is worse than no field at all.
class ChatContent {
  const ChatContent({required this.body});

  final String body;
}

/// Serialises content for encryption. The exact bytes the web client produces — `{"body":"..."}`,
/// UTF-8 — because both ends have to agree on this or every message across clients is gibberish.
Uint8List serializeContent(ChatContent content) =>
    Uint8List.fromList(utf8.encode(jsonEncode({'body': content.body})));

/// Reads decrypted plaintext back into content.
///
/// Tolerant of a missing or malformed body: a message that decrypted correctly but does not parse is
/// a message from a newer client, not a broken one, and showing an empty bubble beats throwing.
ChatContent parseContent(Uint8List plaintext) {
  try {
    final json = jsonDecode(utf8.decode(plaintext));
    if (json is Map && json['body'] is String) {
      return ChatContent(body: json['body'] as String);
    }
  } on FormatException {
    // Fall through.
  }
  return const ChatContent(body: '');
}
