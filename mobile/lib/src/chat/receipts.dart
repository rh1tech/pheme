// What the ticks on your own message mean, worked out from the other members' watermarks.
//
// The server tracks how far each member has got — received up to deliveredAt, read up to readAt —
// rather than a state per message per member. Messages are ordered by createdAt, so "read up to T"
// already says everything about every message at or before T, and a message's ticks are then a
// comparison: delivered once EVERY other member has received it, read once every other member has
// read it. That is the "everyone has read it" rule, and in a 1:1 chat "everyone" is just them.
//
// Mirrors web/src/lib/receipts.ts. Timestamps are ISO-8601 and so compare as strings.

import '../models/chat_models.dart';

/// Nothing yet, one tick (everyone received it), or two (everyone read it).
enum Receipt { sent, delivered, read }

/// The point every member OTHER than [userId] has reached — the oldest of their watermarks, because
/// a message is only through once the slowest of them has it. Empty when one of them has never
/// reported, or when there is nobody else at all.
String _slowest(
  List<ConversationMember> members,
  String userId,
  String Function(ConversationMember) pick,
) {
  var oldest = '';
  for (final member in members) {
    if (member.userId == userId) continue; // never wait on yourself
    final at = pick(member);
    // One member has never reported: nothing is through.
    if (at.isEmpty) {
      return '';
    }
    if (oldest.isEmpty || at.compareTo(oldest) < 0) oldest = at;
  }
  return oldest;
}

/// The ticks for one of YOUR messages.
///
/// Only ever called for your own: a tick on someone else's would be telling them what they already
/// know.
Receipt messageReceipt(
  String createdAt,
  List<ConversationMember> members,
  String userId,
) {
  final others = members.where((m) => m.userId != userId);
  if (others.isEmpty) return Receipt.sent;

  final readUpTo = _slowest(members, userId, (m) => m.readAt);
  if (readUpTo.isNotEmpty && createdAt.compareTo(readUpTo) <= 0) {
    return Receipt.read;
  }

  final deliveredUpTo = _slowest(members, userId, (m) => m.deliveredAt);
  if (deliveredUpTo.isNotEmpty && createdAt.compareTo(deliveredUpTo) <= 0) {
    return Receipt.delivered;
  }

  return Receipt.sent;
}

/// Applies a live receipt to a conversation's members.
///
/// Forward only, mirroring the server: receipts arrive out of order — two devices, a retry, a
/// catch-up — and an older one must not un-read what is already read.
List<ConversationMember> applyReceipt(
  List<ConversationMember> members,
  ConversationReceipt receipt,
) {
  return members
      .map((m) {
        if (m.userId != receipt.userId) return m;
        return m.copyWith(
          deliveredAt: _later(m.deliveredAt, receipt.deliveredAt),
          readAt: _later(m.readAt, receipt.readAt),
        );
      })
      .toList(growable: false);
}

String _later(String a, String b) {
  if (a.isEmpty) return b;
  if (b.isEmpty) return a;
  return b.compareTo(a) > 0 ? b : a;
}
