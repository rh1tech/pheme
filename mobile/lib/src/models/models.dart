// Domain models mirroring the Pheme App API responses (see web/src/lib/types.ts).

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
  });

  final String id;
  final String publicId;
  final String ownerId;
  final String name;
  final SubscriptionMode subscriptionMode;
  final ChannelStatus status;
  final String createdAt;

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
  );
}

class Message {
  Message({
    required this.id,
    required this.channelId,
    required this.title,
    required this.body,
    required this.createdAt,
    this.data,
  });

  final String id;
  final String channelId;
  final String title;
  final String body;
  final String createdAt;
  final Map<String, dynamic>? data;

  factory Message.fromJson(Map<String, dynamic> j) => Message(
    id: j['id'] as String? ?? '',
    channelId: j['channelId'] as String? ?? '',
    title: j['title'] as String? ?? '',
    body: j['body'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    data: (j['data'] as Map?)?.cast<String, dynamic>(),
  );
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

/// Live event delivered over the SSE stream: `{channelId, message}`.
class LiveEvent {
  LiveEvent({required this.channelId, required this.message});

  final String channelId;
  final Message message;

  factory LiveEvent.fromJson(Map<String, dynamic> j) => LiveEvent(
    channelId: j['channelId'] as String? ?? '',
    message: Message.fromJson((j['message'] as Map).cast<String, dynamic>()),
  );
}
