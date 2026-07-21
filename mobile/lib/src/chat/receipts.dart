// What the ticks on your own message mean, worked out from the other members' watermarks.
//
// The server tracks how far each member has got — received up to deliveredSeq, read up to readSeq —
// rather than a state per message per member. Messages are ordered by their per-conversation `seq`,
// so "read up to N" already says everything about every message at or before N, and a message's
// ticks are then a comparison: delivered once EVERY other member has received it, read once every
// other member has read it. That is the "everyone has read it" rule, and in a 1:1 chat "everyone"
// is just them.
//
// Mirrors web/src/lib/receipts.ts. Watermarks are ints; 0 means "never reported".

import '../models/chat_models.dart';

/// Nothing yet, one tick (everyone received it), or two (everyone read it).
enum Receipt { sent, delivered, read }

/// The point every member OTHER than [userId] has reached — the lowest of their watermarks, because
/// a message is only through once the slowest of them has it.
///
/// 0 when there is nobody else (everyone left): there is no one to deliver to, so nothing can be
/// claimed. Also 0 the moment any one member has never reported (watermark 0): nothing is through
/// until the slowest catches up.
int _slowest(
  List<ConversationMember> members,
  String userId,
  int Function(ConversationMember) pick,
) {
  var lowest = 0;
  var seen = false;
  for (final member in members) {
    if (member.userId == userId) continue; // never wait on yourself
    final seq = pick(member);
    // One member has never reported: nothing is through.
    if (seq == 0) return 0;
    if (!seen || seq < lowest) {
      lowest = seq;
      seen = true;
    }
  }
  return lowest;
}

/// The ticks for one of YOUR messages, comparing its [seq] against the other members' watermarks.
///
/// Only ever called for your own: a tick on someone else's would be telling them what they already
/// know.
Receipt messageReceipt(
  int seq,
  List<ConversationMember> members,
  String userId,
) {
  final others = members.where((m) => m.userId != userId);
  if (others.isEmpty) return Receipt.sent;

  final readUpTo = _slowest(members, userId, (m) => m.readSeq);
  if (readUpTo != 0 && seq <= readUpTo) return Receipt.read;

  final deliveredUpTo = _slowest(members, userId, (m) => m.deliveredSeq);
  if (deliveredUpTo != 0 && seq <= deliveredUpTo) return Receipt.delivered;

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
          deliveredSeq: _higher(m.deliveredSeq, receipt.deliveredSeq),
          readSeq: _higher(m.readSeq, receipt.readSeq),
        );
      })
      .toList(growable: false);
}

int _higher(int a, int b) => b > a ? b : a;
