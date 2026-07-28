// The circular avatar used in the list, the header and the members sheet.
//
// The fallback colour is HASHED FROM THE ID, exactly as the web client does it (FNV-1a over the id,
// modulo an eight-colour palette), so the same person is the same colour in both clients and in
// every place they appear. Red and yellow are deliberately absent from the palette: they read as
// status, and a person is not a status.
//
// A direct chat keys on the OTHER user's id rather than the conversation's, so the colour of a chat
// matches the colour of the person in it.

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';

import '../../theme.dart';

const _palette = <Color>[
  kIris,
  kGrape,
  Color(0xFF12B886), // teal
  Color(0xFF15AABF), // cyan
  Color(0xFF228BE6), // blue
  Color(0xFFFD7E14), // orange
  Color(0xFFE64980), // pink
  Color(0xFF82C91E), // lime
];

/// FNV-1a, the same hash the web uses, so the two clients agree on the colour.
Color avatarColor(String id) {
  var hash = 0x811c9dc5;
  for (final unit in id.codeUnits) {
    hash ^= unit;
    hash = (hash * 0x01000193) & 0xFFFFFFFF;
  }
  return _palette[hash % _palette.length];
}

/// The picture to draw for a conversation, or null to fall back to initials.
///
/// A group shows its own picture; a direct chat shows the OTHER person's, which is the same rule
/// the colour follows — a chat and the person in it should look alike wherever they appear.
///
/// This exists because every chat surface drew initials and nothing else. The widget has always
/// taken an [imageUrl] and the server has always sent an avatarId, but the four call sites in the
/// chat — the list, the header, the new-chat sheet and the members sheet — simply never passed
/// one, so a user with a perfectly good profile picture appeared as their initials everywhere
/// except in channels, which did pass it. One helper rather than four copies of the rule.
String? conversationAvatarUrl({
  required bool isGroup,
  required String? groupAvatarId,
  required String? otherAvatarId,
  required String Function(String id) toUrl,
}) {
  final id = isGroup ? groupAvatarId : otherAvatarId;
  return (id == null || id.isEmpty) ? null : toUrl(id);
}

/// Up to two uppercase initials, or '#' when there is nothing to take them from.
String avatarInitials(String label) {
  final words = label.trim().split(RegExp(r'\s+')).where((w) => w.isNotEmpty);
  if (words.isEmpty) return '#';

  final letters = words
      .take(2)
      .map((w) => w.characters.first)
      .join()
      .toUpperCase();
  return letters.isEmpty ? '#' : letters;
}

class ConversationAvatar extends StatelessWidget {
  const ConversationAvatar({
    super.key,
    required this.id,
    required this.label,
    this.imageUrl,
    this.size = 48,
  });

  /// What the colour is hashed from — the other user's id for a direct chat, the group's for a group.
  final String id;
  final String label;
  final String? imageUrl;
  final double size;

  @override
  Widget build(BuildContext context) {
    final url = imageUrl;
    final color = avatarColor(id);

    return SizedBox(
      width: size,
      height: size,
      child: ClipOval(
        child: url != null && url.isNotEmpty
            ? CachedNetworkImage(
                imageUrl: url,
                fit: BoxFit.cover,
                placeholder: (_, _) => ColoredBox(color: color),
                errorWidget: (_, _, _) =>
                    _Initials(color: color, label: label, size: size),
              )
            : _Initials(color: color, label: label, size: size),
      ),
    );
  }
}

class _Initials extends StatelessWidget {
  const _Initials({
    required this.color,
    required this.label,
    required this.size,
  });

  final Color color;
  final String label;
  final double size;

  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: color,
      child: Center(
        child: Text(
          avatarInitials(label),
          style: TextStyle(
            color: Colors.white,
            fontSize: size * 0.36,
            fontWeight: FontWeight.w600,
            height: 1,
          ),
        ),
      ),
    );
  }
}
