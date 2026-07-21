// The conversation half of the API: end-to-end encrypted chat, groups, MLS key material, and call
// signalling. Mirrors web/src/lib/types.ts and api/internal/domain/domain.go.
//
// Bytes cross the wire as base64 strings (Go marshals []byte that way) and are held here as
// Uint8List, decoded once at the boundary. Nothing above this layer should ever see base64.

import 'dart:convert';
import 'dart:typed_data';

import 'models.dart' show ChannelRole, PublicUser;

/// What the ciphertext of a message actually is. The server routes on these and refuses to accept
/// some of them through the ordinary message endpoint, so they are part of the protocol, not a hint.
abstract final class ContentType {
  /// An ordinary chat message: the encrypted `{"body": "..."}` payload.
  static const application = 'application/mls';

  /// An MLS Welcome. Posted to /mls/commit, never to /messages.
  static const mlsWelcome = 'application/mls-welcome';

  /// An MLS Commit. Posted to /mls/commit, never to /messages.
  static const mlsCommit = 'application/mls-commit';

  /// "I am a member of this conversation and my device holds no group — admit me."
  static const mlsDevice = 'application/mls-device';

  /// "I just joined and hold none of this conversation's history — can a co-member send it?"
  static const mlsHistoryRequest = 'application/mls-history-request';

  /// "Here is the sealed history you asked for" — points at an uploaded blob, addressed to one device.
  static const mlsHistoryOffer = 'application/mls-history-offer';

  /// A missed/declined/failed call, recorded as a real encrypted message.
  static const callEvent = 'application/pheme-call-event';

  /// Someone joined or left. Written by the SERVER, in plaintext, because the server holds no keys
  /// and a roster change is already visible to every member from the member list. It is the one
  /// message in a conversation that is not encrypted, and it never carries anything a person wrote.
  static const membership = 'application/pheme-membership';

  /// Control traffic: never rendered as a message, never decrypted as one.
  static const control = {
    mlsWelcome,
    mlsCommit,
    mlsDevice,
    mlsHistoryRequest,
    mlsHistoryOffer,
  };

  /// Rendered, but not as somebody's message: no bubble, no sender, no decryption. Kept OUT of
  /// [control], because control traffic is filtered out of the feed entirely and this has to
  /// appear in it.
  static const system = {membership};
}

enum ConversationKind {
  direct,
  group;

  String get wire => name;

  static ConversationKind fromWire(String? v) =>
      v == 'group' ? ConversationKind.group : ConversationKind.direct;
}

/// One member of a conversation, with their public profile hydrated by the server.
class ConversationMember {
  ConversationMember({
    required this.id,
    required this.conversationId,
    required this.userId,
    required this.role,
    required this.joinedAt,
    required this.user,
    this.deliveredSeq = 0,
    this.readSeq = 0,
    this.joinSeq = 0,
  });

  final String id;
  final String conversationId;
  final String userId;
  final ChannelRole role;
  final String joinedAt;
  final PublicUser user;

  /// How far this member has got: they have RECEIVED every message up to [deliveredSeq] and READ
  /// every message up to [readSeq]. Watermarks, not per-message state — messages are ordered by
  /// their per-conversation `seq`, so "read up to N" already covers every message at or before N,
  /// and the ticks on your own message are a comparison against the other members' (see
  /// messageReceipt).
  ///
  /// 0 for a member who has not reported since joining.
  final int deliveredSeq;
  final int readSeq;

  /// The conversation sequence when this member joined — the floor their watermarks start at. 0 for
  /// a member present from the start.
  final int joinSeq;

  bool get isAdmin => role == ChannelRole.admin;

  ConversationMember copyWith({int? deliveredSeq, int? readSeq}) =>
      ConversationMember(
        id: id,
        conversationId: conversationId,
        userId: userId,
        role: role,
        joinedAt: joinedAt,
        user: user,
        deliveredSeq: deliveredSeq ?? this.deliveredSeq,
        readSeq: readSeq ?? this.readSeq,
        joinSeq: joinSeq,
      );

  factory ConversationMember.fromJson(Map<String, dynamic> j) =>
      ConversationMember(
        id: j['id'] as String? ?? '',
        conversationId: j['conversationId'] as String? ?? '',
        userId: j['userId'] as String? ?? '',
        role: ChannelRole.fromWire(j['role'] as String?),
        joinedAt: j['joinedAt'] as String? ?? '',
        user: PublicUser.fromJson(
          ((j['user'] as Map?) ?? const {}).cast<String, dynamic>(),
        ),
        deliveredSeq: (j['deliveredSeq'] as num?)?.toInt() ?? 0,
        readSeq: (j['readSeq'] as num?)?.toInt() ?? 0,
        joinSeq: (j['joinSeq'] as num?)?.toInt() ?? 0,
      );
}

/// One member's receipt watermarks moving, as the live stream reports them. It says how far
/// someone has got, never what they read.
class ConversationReceipt {
  const ConversationReceipt({
    required this.userId,
    this.deliveredSeq = 0,
    this.readSeq = 0,
  });

  final String userId;
  final int deliveredSeq;
  final int readSeq;

  factory ConversationReceipt.fromJson(Map<String, dynamic> j) =>
      ConversationReceipt(
        userId: j['userId'] as String? ?? '',
        deliveredSeq: (j['deliveredSeq'] as num?)?.toInt() ?? 0,
        readSeq: (j['readSeq'] as num?)?.toInt() ?? 0,
      );
}

/// A direct or group conversation.
class Conversation {
  Conversation({
    required this.id,
    required this.kind,
    required this.createdBy,
    required this.createdAt,
    this.title,
    this.avatarId,
    this.members = const [],
    this.lastMessage,
  });

  final String id;
  final ConversationKind kind;
  final String createdBy;
  final String createdAt;
  final String? title;
  final String? avatarId;
  final List<ConversationMember> members;

  /// The newest message, for the conversation list. Still ciphertext: the list cannot decrypt it,
  /// so a preview has to come from the local plaintext cache.
  final LastChatMessage? lastMessage;

  bool get isGroup => kind == ConversationKind.group;

  /// The same conversation with a new member list — for moving a receipt watermark without
  /// rebuilding every field by hand at the call site.
  Conversation withMembers(List<ConversationMember> members) => Conversation(
    id: id,
    kind: kind,
    createdBy: createdBy,
    createdAt: createdAt,
    title: title,
    avatarId: avatarId,
    members: members,
    lastMessage: lastMessage,
  );

  /// The member row for [userId], or null if they are not in this conversation.
  ConversationMember? memberOf(String userId) {
    for (final m in members) {
      if (m.userId == userId) return m;
    }
    return null;
  }

  bool isAdmin(String userId) => memberOf(userId)?.isAdmin ?? false;

  /// The other person in a direct chat. Null for a group, or if the roster has not loaded.
  ConversationMember? otherMember(String myUserId) {
    for (final m in members) {
      if (m.userId != myUserId) return m;
    }
    return null;
  }

  factory Conversation.fromJson(Map<String, dynamic> j) => Conversation(
    id: j['id'] as String? ?? '',
    kind: ConversationKind.fromWire(j['kind'] as String?),
    createdBy: j['createdBy'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    title: j['title'] as String?,
    avatarId: j['avatarId'] as String?,
    members: ((j['members'] as List?) ?? const [])
        .map(
          (e) =>
              ConversationMember.fromJson((e as Map).cast<String, dynamic>()),
        )
        .toList(),
    lastMessage: (j['lastMessage'] as Map?) == null
        ? null
        : LastChatMessage.fromJson(
            (j['lastMessage'] as Map).cast<String, dynamic>(),
          ),
  );
}

/// The trailing message on a conversation summary.
class LastChatMessage {
  LastChatMessage({
    required this.id,
    required this.senderId,
    required this.ciphertext,
    required this.contentType,
    required this.createdAt,
  });

  final String id;
  final String senderId;
  final Uint8List ciphertext;
  final String contentType;
  final String createdAt;

  factory LastChatMessage.fromJson(Map<String, dynamic> j) => LastChatMessage(
    id: j['id'] as String? ?? '',
    senderId: j['senderId'] as String? ?? '',
    ciphertext: _bytes(j['ciphertext']),
    contentType: j['contentType'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
  );
}

/// One message in a conversation. The server only ever holds the ciphertext.
class ChatMessage {
  ChatMessage({
    required this.id,
    required this.conversationId,
    required this.senderId,
    required this.ciphertext,
    required this.contentType,
    required this.createdAt,
    this.seq = 0,
    this.mlsEpoch,
  });

  final String id;
  final String conversationId;
  final String senderId;
  final Uint8List ciphertext;
  final String contentType;
  final String createdAt;

  /// This message's per-conversation sequence, assigned by the hub. Monotonic, and the order the
  /// receipt watermarks are compared against (see messageReceipt). 0 for messages predating
  /// sequencing — they have no watermark to move.
  final int seq;

  /// Set only on a Welcome or a Commit: the epoch it takes the group to.
  final int? mlsEpoch;

  /// Control traffic, not something to render or decrypt as a message.
  bool get isControl => ContentType.control.contains(contentType);

  /// A line the conversation shows about itself — someone joined, someone left. Not encrypted and
  /// not written by a person, so it must never be handed to the decrypt path: doing so would burn
  /// a lookup and render it as "not available on this device".
  bool get isSystem => ContentType.system.contains(contentType);

  /// The membership change this line describes, or null if it is not one.
  MembershipEvent? get membershipEvent => contentType == ContentType.membership
      ? MembershipEvent.tryParse(ciphertext)
      : null;

  factory ChatMessage.fromJson(Map<String, dynamic> j) => ChatMessage(
    id: j['id'] as String? ?? '',
    conversationId: j['conversationId'] as String? ?? '',
    senderId: j['senderId'] as String? ?? '',
    ciphertext: _bytes(j['ciphertext']),
    contentType: j['contentType'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    seq: (j['seq'] as num?)?.toInt() ?? 0,
    mlsEpoch: (j['mlsEpoch'] as num?)?.toInt(),
  );

  /// Round-trips through [ChatMessage.fromJson] — ciphertext as base64, matching the
  /// wire shape — so the message envelope can be cached to disk and read back. Used by
  /// the envelope cache, never sent to the server (the server assigns ids and times).
  Map<String, dynamic> toJson() => {
    'id': id,
    'conversationId': conversationId,
    'senderId': senderId,
    'ciphertext': base64Encode(ciphertext),
    'contentType': contentType,
    'createdAt': createdAt,
    if (seq != 0) 'seq': seq,
    if (mlsEpoch != null) 'mlsEpoch': mlsEpoch,
  };
}

/// One page of conversation history, walked backwards in time.
class ChatMessagesPage {
  ChatMessagesPage({required this.messages, this.nextCursor});

  /// Newest-first, as the server returns them.
  final List<ChatMessage> messages;

  /// The id to pass as `cursor` for the next (older) page. Null at the start of history.
  final String? nextCursor;
}

// --- MLS ---------------------------------------------------------------------------------------

/// Where the conversation's MLS group has got to, as the server sees it.
///
/// The server is an untrusted delivery service: it cannot read a Commit (they are PrivateMessage
/// framed), but it still has to answer "which group is this?" and "whose Commit came first?".
class MLSGroupState {
  MLSGroupState({
    required this.groupId,
    required this.epoch,
    this.priorGroupIds = const [],
  });

  /// Empty until a group has been established. Set exactly once.
  final String groupId;

  /// The last Commit the server accepted. A Commit is proposed against this, and the server
  /// compare-and-sets on it.
  final int epoch;

  /// Groups that were retired by a reset. Still decryptable — messages sent under them must not
  /// become unreadable just because the group was rebuilt.
  final List<String> priorGroupIds;

  bool get isEstablished => groupId.isNotEmpty;

  /// Every group this conversation's history could have been encrypted under, newest first.
  List<String> get allGroupIds => [
    if (isEstablished) groupId,
    ...priorGroupIds,
  ];

  factory MLSGroupState.fromJson(Map<String, dynamic> j) => MLSGroupState(
    groupId: j['groupId'] as String? ?? '',
    epoch: (j['epoch'] as num?)?.toInt() ?? 0,
    priorGroupIds: ((j['priorGroupIds'] as List?) ?? const [])
        .map((e) => e as String)
        .toList(),
  );
}

/// The outcome of offering a Commit to the server.
///
/// A rejection is deliberately not an exception. Two members can stage a Commit against the same
/// epoch and only one of them can win; losing is an ordinary, expected outcome of a healthy group,
/// and the loser's job is to discard its Commit — never to apply it — catch up on the winning one,
/// and try again. Modelling that as a thrown error invites a `catch` that treats it as failure.
class MlsCommitResult {
  MlsCommitResult({required this.accepted, required this.state});

  final bool accepted;

  /// The group state that is now authoritative: ours if we won, the winner's if we lost.
  final MLSGroupState state;
}

/// One device of one user. An MLS leaf is a device, not a person.
class MLSDeviceRef {
  MLSDeviceRef({required this.userId, required this.deviceId});

  final String userId;
  final String deviceId;

  /// The MLS credential identity: `userId:deviceId`.
  String get identity => '$userId:$deviceId';

  Map<String, dynamic> toJson() => {'userId': userId, 'deviceId': deviceId};

  @override
  bool operator ==(Object other) =>
      other is MLSDeviceRef &&
      other.userId == userId &&
      other.deviceId == deviceId;

  @override
  int get hashCode => Object.hash(userId, deviceId);
}

/// A KeyPackage claimed for one device, ready to be added to a group.
class MLSClaimedKeyPackage {
  MLSClaimedKeyPackage({
    required this.userId,
    required this.deviceId,
    required this.keyPackage,
  });

  final String userId;
  final String deviceId;
  final Uint8List keyPackage;

  factory MLSClaimedKeyPackage.fromJson(Map<String, dynamic> j) =>
      MLSClaimedKeyPackage(
        userId: j['userId'] as String? ?? '',
        deviceId: j['deviceId'] as String? ?? '',
        keyPackage: _bytes(j['keyPackage']),
      );
}

/// How many single-use key packages a device has left on the server.
class MLSKeyPackageCount {
  MLSKeyPackageCount({required this.count, required this.hasLastResort});

  final int count;
  final bool hasLastResort;

  factory MLSKeyPackageCount.fromJson(Map<String, dynamic> j) =>
      MLSKeyPackageCount(
        count: (j['count'] as num?)?.toInt() ?? 0,
        hasLastResort: j['hasLastResort'] as bool? ?? false,
      );
}

/// The passphrase-sealed client state held server-side, so a lost device is recoverable. The server
/// sees these three fields and never the passphrase or the plaintext.
/// One of the user's own devices, as shown in "your devices": its client-minted MLS device id, a
/// human label, and when it was last active. Carries no key material.
class MLSDevice {
  MLSDevice({
    required this.deviceId,
    required this.label,
    required this.createdAt,
    required this.lastSeenAt,
  });

  final String deviceId;
  final String label;
  final String createdAt;
  final String lastSeenAt;

  factory MLSDevice.fromJson(Map<String, dynamic> j) => MLSDevice(
    deviceId: (j['deviceId'] as String?) ?? '',
    label: (j['label'] as String?) ?? '',
    createdAt: (j['createdAt'] as String?) ?? '',
    lastSeenAt: (j['lastSeenAt'] as String?) ?? '',
  );
}

class MLSKeyBackup {
  MLSKeyBackup({
    required this.salt,
    required this.nonce,
    required this.ciphertext,
    this.transcriptSalt,
    this.transcriptNonce,
    this.transcriptCiphertext,
    this.updatedAt,
  });

  final Uint8List salt;
  final Uint8List nonce;
  final Uint8List ciphertext;

  /// The sealed transcript, if this backup carries one. Its own salt and nonce: one recovery
  /// secret, two independent seals, so either blob can be replaced without re-encrypting the other.
  /// Null when the backup is key-state only (a legacy backup, or one taken before the transcript
  /// was ever cached).
  final Uint8List? transcriptSalt;
  final Uint8List? transcriptNonce;
  final Uint8List? transcriptCiphertext;

  final String? updatedAt;

  /// True when this backup carries a transcript that can actually be opened.
  bool get hasTranscript =>
      (transcriptCiphertext?.isNotEmpty ?? false) &&
      (transcriptSalt?.isNotEmpty ?? false) &&
      (transcriptNonce?.isNotEmpty ?? false);

  factory MLSKeyBackup.fromJson(Map<String, dynamic> j) {
    final ts = _bytes(j['transcriptSalt']);
    final tn = _bytes(j['transcriptNonce']);
    final tc = _bytes(j['transcriptCiphertext']);
    return MLSKeyBackup(
      salt: _bytes(j['salt']),
      nonce: _bytes(j['nonce']),
      ciphertext: _bytes(j['ciphertext']),
      transcriptSalt: ts.isEmpty ? null : ts,
      transcriptNonce: tn.isEmpty ? null : tn,
      transcriptCiphertext: tc.isEmpty ? null : tc,
      updatedAt: j['updatedAt'] as String?,
    );
  }
}

// --- Calls -------------------------------------------------------------------------------------

/// One signal from the server's ordered mailbox. `seq` is monotonic per call, so a client reads
/// from a cursor and can never miss one — which is the whole reason the mailbox exists.
class CallSignal {
  CallSignal({required this.seq, required this.ciphertext});

  final int seq;

  /// The sealed envelope. Still base64-of-JSON at this point: see call_envelope.dart.
  final Uint8List ciphertext;

  factory CallSignal.fromJson(Map<String, dynamic> j) => CallSignal(
    seq: (j['seq'] as num?)?.toInt() ?? 0,
    ciphertext: _bytes(j['ciphertext']),
  );
}

/// A STUN/TURN server for WebRTC. Fetched per call and never cached: the credentials are
/// short-lived (an HMAC over an expiry timestamp).
class IceServer {
  IceServer({required this.urls, this.username, this.credential});

  final List<String> urls;
  final String? username;
  final String? credential;

  Map<String, dynamic> toJson() => {
    'urls': urls,
    if (username != null) 'username': username,
    if (credential != null) 'credential': credential,
  };

  factory IceServer.fromJson(Map<String, dynamic> j) {
    final urls = j['urls'];
    return IceServer(
      urls: urls is List
          ? urls.map((e) => e as String).toList()
          : [if (urls is String) urls],
      username: j['username'] as String?,
      credential: j['credential'] as String?,
    );
  }
}

/// Decodes a base64 field into bytes. Go marshals []byte as a base64 string, and a missing or null
/// field means empty, not an error.
Uint8List _bytes(Object? v) {
  if (v is String && v.isNotEmpty) return base64Decode(v);
  return Uint8List(0);
}

/// A roster change, as written by the server in plaintext.
///
/// Parsed defensively: an unknown action or a malformed body yields null and the line is simply not
/// shown, which is better than a conversation refusing to render because of a note about it.
class MembershipEvent {
  const MembershipEvent({
    required this.action,
    required this.actorId,
    required this.userId,
  });

  /// "added", "removed", or "left".
  final String action;

  /// Who did it. Equal to [userId] when somebody left of their own accord.
  final String actorId;

  /// Who it happened to.
  final String userId;

  static const _actions = {'added', 'removed', 'left'};

  static MembershipEvent? tryParse(Uint8List bytes) {
    try {
      final decoded = jsonDecode(utf8.decode(bytes));
      if (decoded is! Map) return null;
      final action = decoded['action'];
      final userId = decoded['userId'];
      if (action is! String || !_actions.contains(action)) return null;
      if (userId is! String || userId.isEmpty) return null;
      return MembershipEvent(
        action: action,
        actorId: decoded['actorId'] as String? ?? '',
        userId: userId,
      );
    } on Object {
      return null;
    }
  }
}
