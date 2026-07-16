// Domain models mirroring the Pheme App API responses (see web/src/lib/types.ts).

import 'chat_models.dart' show ChatMessage, ConversationReceipt;

enum SubscriptionMode {
  open,
  approval;

  String get wire => name;

  static SubscriptionMode fromWire(String? v) =>
      v == 'open' ? SubscriptionMode.open : SubscriptionMode.approval;
}

enum ChannelStatus {
  active,
  disabled;

  static ChannelStatus fromWire(String? v) =>
      v == 'disabled' ? ChannelStatus.disabled : ChannelStatus.active;
}

/// Subscription state of a single device against a channel.
enum SubscriptionStatus { active, pending, none }

SubscriptionStatus subscriptionStatusFromWire(String? v) {
  switch (v) {
    case 'active':
      return SubscriptionStatus.active;
    case 'pending':
      return SubscriptionStatus.pending;
    default:
      return SubscriptionStatus.none;
  }
}

/// A member's role within a channel.
enum ChannelRole {
  user,
  admin;

  String get wire => name;

  static ChannelRole fromWire(String? v) =>
      v == 'admin' ? ChannelRole.admin : ChannelRole.user;
}

/// Membership state of a member within a channel.
enum MemberStatus {
  active,
  pending,
  blocked;

  String get wire => name;

  static MemberStatus fromWire(String? v) {
    switch (v) {
      case 'pending':
        return MemberStatus.pending;
      case 'blocked':
        return MemberStatus.blocked;
      default:
        return MemberStatus.active;
    }
  }
}

/// The caller's relationship to a channel, as returned by `GET /channels/{id}`
/// and `GET /channels/{id}/membership`. [status] is `none` when the caller is
/// the owner (not a member) or has no membership row.
enum MembershipStatus {
  active,
  pending,
  blocked,
  none;

  static MembershipStatus fromWire(String? v) {
    switch (v) {
      case 'active':
        return MembershipStatus.active;
      case 'pending':
        return MembershipStatus.pending;
      case 'blocked':
        return MembershipStatus.blocked;
      default:
        return MembershipStatus.none;
    }
  }
}

class TokenResponse {
  TokenResponse({
    required this.accessToken,
    required this.refreshToken,
    required this.userId,
    required this.role,
  });

  final String accessToken;
  final String refreshToken;
  final String userId;
  final String role;

  factory TokenResponse.fromJson(Map<String, dynamic> j) => TokenResponse(
    accessToken: j['accessToken'] as String? ?? '',
    refreshToken: j['refreshToken'] as String? ?? '',
    userId: j['userId'] as String? ?? '',
    role: j['role'] as String? ?? 'user',
  );
}

class Channel {
  Channel({
    required this.id,
    required this.publicId,
    required this.ownerId,
    required this.name,
    required this.subscriptionMode,
    required this.status,
    required this.createdAt,
    this.alias,
  });

  final String id;
  final String publicId;
  final String ownerId;
  final String name;
  final SubscriptionMode subscriptionMode;
  final ChannelStatus status;
  final String createdAt;

  /// The channel's optional "phetag" — a human-friendly handle that can be used
  /// in place of [publicId] when joining. Null or empty when unset.
  final String? alias;

  /// The shareable reference others use to join: the [alias] when set,
  /// otherwise the [publicId].
  String get joinRef =>
      (alias != null && alias!.isNotEmpty) ? alias! : publicId;

  factory Channel.fromJson(Map<String, dynamic> j) => Channel(
    id: j['id'] as String? ?? '',
    publicId: j['publicId'] as String? ?? '',
    ownerId: j['ownerId'] as String? ?? '',
    name: j['name'] as String? ?? '',
    subscriptionMode: SubscriptionMode.fromWire(
      j['subscriptionMode'] as String?,
    ),
    status: ChannelStatus.fromWire(j['status'] as String?),
    createdAt: j['createdAt'] as String? ?? '',
    alias: j['alias'] as String?,
  );
}

/// The caller's relationship to a channel, returned by `GET /channels/{id}`.
class ChannelRelation {
  ChannelRelation({
    required this.channel,
    required this.isOwner,
    required this.role,
    required this.status,
  });

  final Channel channel;
  final bool isOwner;
  final ChannelRole role;
  final MembershipStatus status;

  /// True when the caller may manage subscribers (owner or admin).
  bool get canManage => isOwner || role == ChannelRole.admin;

  factory ChannelRelation.fromJson(Map<String, dynamic> j) => ChannelRelation(
    channel: Channel.fromJson((j['channel'] as Map).cast<String, dynamic>()),
    isOwner: j['isOwner'] as bool? ?? false,
    role: ChannelRole.fromWire(j['role'] as String?),
    status: MembershipStatus.fromWire(j['status'] as String?),
  );
}

/// A channel the caller has joined (not owned), returned by
/// `GET /channels/joined`: the channel fields plus the caller's role and
/// membership status (the wire field is `memberStatus`, not `status`).
class JoinedChannel {
  JoinedChannel({
    required this.channel,
    required this.role,
    required this.memberStatus,
  });

  final Channel channel;
  final ChannelRole role;
  final MemberStatus memberStatus;

  factory JoinedChannel.fromJson(Map<String, dynamic> j) => JoinedChannel(
    channel: Channel.fromJson(j),
    role: ChannelRole.fromWire(j['role'] as String?),
    memberStatus: MemberStatus.fromWire(j['memberStatus'] as String?),
  );
}

/// A subscriber/member of a channel, returned by the approvals and members
/// endpoints.
class ChannelMember {
  ChannelMember({
    required this.id,
    required this.channelId,
    required this.userId,
    required this.email,
    required this.role,
    required this.status,
    required this.createdAt,
  });

  final String id;
  final String channelId;
  final String userId;
  final String email;
  final ChannelRole role;
  final MemberStatus status;
  final String createdAt;

  factory ChannelMember.fromJson(Map<String, dynamic> j) => ChannelMember(
    id: j['id'] as String? ?? '',
    channelId: j['channelId'] as String? ?? '',
    userId: j['userId'] as String? ?? '',
    email: j['email'] as String? ?? '',
    role: ChannelRole.fromWire(j['role'] as String?),
    status: MemberStatus.fromWire(j['status'] as String?),
    createdAt: j['createdAt'] as String? ?? '',
  );
}

/// A processed image attached to a message. Width/height are the final pixel
/// dimensions, used to reserve aspect ratio before the image loads.
class MessageImage {
  MessageImage({required this.id, required this.width, required this.height});

  final String id;
  final int width;
  final int height;

  double get aspectRatio => height > 0 ? width / height : 1;

  factory MessageImage.fromJson(Map<String, dynamic> j) => MessageImage(
    id: j['id'] as String? ?? '',
    width: (j['width'] as num?)?.toInt() ?? 0,
    height: (j['height'] as num?)?.toInt() ?? 0,
  );
}

class Message {
  Message({
    required this.id,
    required this.channelId,
    required this.title,
    required this.body,
    required this.createdAt,
    this.images = const [],
    this.data,
    this.commentsAllowed = true,
  });

  final String id;
  final String channelId;
  final String title;
  final String body;
  final String createdAt;
  final List<MessageImage> images;
  final Map<String, dynamic>? data;

  /// Whether members may comment on this message (decided per-message when
  /// sending; defaults to true for older payloads without the field).
  final bool commentsAllowed;

  factory Message.fromJson(Map<String, dynamic> j) => Message(
    id: j['id'] as String? ?? '',
    channelId: j['channelId'] as String? ?? '',
    title: j['title'] as String? ?? '',
    body: j['body'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    images: ((j['images'] as List?) ?? const [])
        .map((e) => MessageImage.fromJson((e as Map).cast<String, dynamic>()))
        .toList(),
    data: (j['data'] as Map?)?.cast<String, dynamic>(),
    commentsAllowed: j['commentsAllowed'] as bool? ?? true,
  );
}

/// The non-sensitive public profile of a user (e.g. a comment author).
class PublicUser {
  PublicUser({
    required this.id,
    this.username,
    this.displayName,
    this.avatarId,
  });

  final String id;
  final String? username;
  final String? displayName;
  final String? avatarId;

  /// A human label for display: the display name, else the username, else a
  /// caller-provided fallback.
  String label(String fallback) {
    if (displayName != null && displayName!.isNotEmpty) return displayName!;
    if (username != null && username!.isNotEmpty) return username!;
    return fallback;
  }

  factory PublicUser.fromJson(Map<String, dynamic> j) => PublicUser(
    id: j['id'] as String? ?? '',
    username: j['username'] as String?,
    displayName: j['displayName'] as String?,
    avatarId: j['avatarId'] as String?,
  );
}

/// The authenticated user's own account and profile (`GET/PATCH /v1/me`).
class User {
  User({
    required this.id,
    required this.email,
    required this.role,
    this.username,
    this.displayName,
    this.bio,
    this.phone,
    this.website,
    this.avatarId,
  });

  final String id;
  final String email;
  final String role;
  final String? username;
  final String? displayName;
  final String? bio;
  final String? phone;
  final String? website;
  final String? avatarId;

  factory User.fromJson(Map<String, dynamic> j) => User(
    id: j['id'] as String? ?? '',
    email: j['email'] as String? ?? '',
    role: j['role'] as String? ?? 'user',
    username: j['username'] as String?,
    displayName: j['displayName'] as String?,
    bio: j['bio'] as String?,
    phone: j['phone'] as String?,
    website: j['website'] as String?,
    avatarId: j['avatarId'] as String?,
  );
}

/// A comment on a message, with its author's public profile.
class Comment {
  Comment({
    required this.id,
    required this.messageId,
    required this.channelId,
    required this.userId,
    required this.body,
    required this.createdAt,
    required this.author,
  });

  final String id;
  final String messageId;
  final String channelId;
  final String userId;
  final String body;
  final String createdAt;
  final PublicUser author;

  factory Comment.fromJson(Map<String, dynamic> j) => Comment(
    id: j['id'] as String? ?? '',
    messageId: j['messageId'] as String? ?? '',
    channelId: j['channelId'] as String? ?? '',
    userId: j['userId'] as String? ?? '',
    body: j['body'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    author: PublicUser.fromJson(
      ((j['author'] as Map?) ?? const {}).cast<String, dynamic>(),
    ),
  );
}

class CommentsPage {
  CommentsPage({required this.comments, required this.nextCursor});

  final List<Comment> comments;
  final String nextCursor;
}

class MessagesPage {
  MessagesPage({required this.messages, required this.nextCursor});

  final List<Message> messages;
  final String nextCursor;
}

class ApiKey {
  ApiKey({
    required this.id,
    required this.channelId,
    required this.prefix,
    required this.label,
    required this.createdAt,
    this.revokedAt,
  });

  final String id;
  final String channelId;
  final String prefix;
  final String label;
  final String createdAt;
  final String? revokedAt;

  bool get revoked => revokedAt != null && revokedAt!.isNotEmpty;

  factory ApiKey.fromJson(Map<String, dynamic> j) => ApiKey(
    id: j['id'] as String? ?? '',
    channelId: j['channelId'] as String? ?? '',
    prefix: j['prefix'] as String? ?? '',
    label: j['label'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    revokedAt: j['revokedAt'] as String?,
  );
}

class CreatedKey {
  CreatedKey({
    required this.id,
    required this.key,
    required this.prefix,
    required this.note,
  });

  final String id;
  final String key;
  final String prefix;
  final String note;

  factory CreatedKey.fromJson(Map<String, dynamic> j) => CreatedKey(
    id: j['id'] as String? ?? '',
    key: j['key'] as String? ?? '',
    prefix: j['prefix'] as String? ?? '',
    note: j['note'] as String? ?? '',
  );
}

class Device {
  Device({required this.id, required this.userId, required this.platform});

  final String id;
  final String userId;
  final String platform;

  factory Device.fromJson(Map<String, dynamic> j) => Device(
    id: j['id'] as String? ?? '',
    userId: j['userId'] as String? ?? '',
    platform: j['platform'] as String? ?? '',
  );
}

/// A live event delivered over the SSE stream.
///
/// One event name (`message`) carries four different shapes, told apart by which fields are present
/// — see `live.Event` in api/internal/live/live.go. Every field is therefore nullable, and nothing
/// may assume the shape it happens to be interested in:
///
///   * a channel broadcast    `{channelId, message}`
///   * a conversation message `{conversationId, chatMessage}`  (includes MLS control traffic)
///   * a conversation deleted `{conversationId, conversationDeleted}`
///   * a call nudge           `{conversationId, callSignal}`
///
/// This used to require `message` and throw on everything else, and `SseClient` swallowed the
/// exception — so every chat message and every incoming call was silently dropped on the floor.
class LiveEvent {
  LiveEvent({
    this.channelId,
    this.message,
    this.conversationId,
    this.chatMessage,
    this.conversationDeleted = false,
    this.receipt,
    this.callSignal,
  });

  final String? channelId;
  final Message? message;

  final String? conversationId;
  final ChatMessage? chatMessage;
  final bool conversationDeleted;

  /// A member's receipt watermarks moved: they have received (or read) up to here. Carries
  /// conversationId, and moves the sender's ticks without a refetch.
  final ConversationReceipt? receipt;

  /// A nudge, NOT the signal itself. The live bus is allowed to drop events for slow consumers, and
  /// a dropped SDP answer is a call that never connects — so the signal of record lives in the
  /// server's ordered mailbox and this only says "go read it".
  final CallSignalNudge? callSignal;

  factory LiveEvent.fromJson(Map<String, dynamic> j) {
    final message = j['message'] as Map?;
    final chatMessage = j['chatMessage'] as Map?;
    final callSignal = j['callSignal'] as Map?;
    final receipt = j['receipt'] as Map?;

    return LiveEvent(
      channelId: j['channelId'] as String?,
      message: message == null
          ? null
          : Message.fromJson(message.cast<String, dynamic>()),
      conversationId: j['conversationId'] as String?,
      chatMessage: chatMessage == null
          ? null
          : ChatMessage.fromJson(chatMessage.cast<String, dynamic>()),
      conversationDeleted: j['conversationDeleted'] as bool? ?? false,
      receipt: receipt == null
          ? null
          : ConversationReceipt.fromJson(receipt.cast<String, dynamic>()),
      callSignal: callSignal == null
          ? null
          : CallSignalNudge.fromJson(callSignal.cast<String, dynamic>()),
    );
  }
}

/// The `callSignal` field of a [LiveEvent]: which call moved, and how far.
class CallSignalNudge {
  CallSignalNudge({
    required this.callId,
    required this.seq,
    required this.fromUserId,
  });

  final String callId;
  final int seq;

  /// The sending user. Our own other devices must not ring for our own call.
  final String fromUserId;

  factory CallSignalNudge.fromJson(Map<String, dynamic> j) => CallSignalNudge(
    callId: j['callId'] as String? ?? '',
    seq: (j['seq'] as num?)?.toInt() ?? 0,
    fromUserId: j['fromUserId'] as String? ?? '',
  );
}
