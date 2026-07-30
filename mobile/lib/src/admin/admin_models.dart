// The admin API's payloads, as the app sees them.
//
// Deliberately separate from models/models.dart: everything in there is something a normal account
// can ask for, and everything here is refused with a 403 unless the caller holds the admin role.
// Keeping the two apart makes it obvious at the import line which surface a screen is talking to.
//
// Mirrors web/src/lib/types.ts. Every field is read defensively — an older server is entitled to
// omit fields this build knows about, and an admin screen that throws while decoding is worse than
// one showing a blank column.

import '../models/models.dart';

/// One page of a paginated admin listing.
class AdminPage<T> {
  const AdminPage({
    required this.items,
    required this.total,
    required this.page,
    required this.limit,
  });

  const AdminPage.empty() : items = const [], total = 0, page = 1, limit = 20;

  final List<T> items;
  final int total;
  final int page;
  final int limit;

  /// Whether a page after this one exists. Derived rather than sent: the server reports the total,
  /// and the arithmetic belongs in one place instead of at every call site.
  bool get hasMore => page * limit < total;
}

/// A user row in the admin list: the account, plus how many channels they own.
class AdminUser {
  const AdminUser({
    required this.id,
    required this.email,
    required this.role,
    required this.status,
    required this.createdAt,
    required this.channelCount,
    this.displayName,
    this.username,
  });

  final String id;
  final String email;

  /// 'admin' or 'user'.
  final String role;

  /// 'active', 'blocked' or 'disabled'. Empty on accounts that predate the field, which the
  /// server backfills as active — done here too so the UI never renders a blank status.
  final String status;
  final String createdAt;
  final int channelCount;
  final String? displayName;
  final String? username;

  bool get isAdmin => role == 'admin';
  bool get isBlocked => status == 'blocked';
  bool get isDisabled => status == 'disabled';

  /// What to call this account on screen. The email is what an admin searches by and the only
  /// field guaranteed present, so it is the fallback rather than the other way round.
  String get label => (displayName?.isNotEmpty ?? false) ? displayName! : email;

  factory AdminUser.fromJson(Map<String, dynamic> j) => AdminUser(
    id: j['id'] as String? ?? '',
    email: j['email'] as String? ?? '',
    role: j['role'] as String? ?? 'user',
    status: (j['status'] as String?)?.isNotEmpty == true
        ? j['status'] as String
        : 'active',
    createdAt: j['createdAt'] as String? ?? '',
    channelCount: (j['channelCount'] as num?)?.toInt() ?? 0,
    displayName: j['displayName'] as String?,
    username: j['username'] as String?,
  );
}

/// A channel row in the admin list: the channel, plus its owner's email.
class AdminChannel {
  const AdminChannel({required this.channel, required this.ownerEmail});

  final Channel channel;

  /// Empty when the owner's account has since been deleted — which happens, and must render as a
  /// dash rather than as a crash.
  final String ownerEmail;

  String get id => channel.id;
  String get name => channel.name;
  bool get isDisabled => channel.status == ChannelStatus.disabled;

  factory AdminChannel.fromJson(Map<String, dynamic> j) => AdminChannel(
    channel: Channel.fromJson(j),
    ownerEmail: j['ownerEmail'] as String? ?? '',
  );
}

/// A comment as the moderation list shows it: the comment, hydrated with who wrote it and where.
class AdminComment {
  const AdminComment({
    required this.id,
    required this.body,
    required this.createdAt,
    required this.authorId,
    required this.authorEmail,
    required this.channelName,
    required this.messageTitle,
  });

  final String id;
  final String body;
  final String createdAt;
  final String authorId;
  final String authorEmail;
  final String channelName;
  final String messageTitle;

  factory AdminComment.fromJson(Map<String, dynamic> j) => AdminComment(
    id: j['id'] as String? ?? '',
    body: j['body'] as String? ?? '',
    createdAt: j['createdAt'] as String? ?? '',
    authorId: j['authorId'] as String? ?? '',
    authorEmail: j['authorEmail'] as String? ?? '',
    channelName: j['channelName'] as String? ?? '',
    messageTitle: j['messageTitle'] as String? ?? '',
  );
}

/// The status of an invitation. Mirrors domain.InviteStatus on the server.
enum InviteStatus {
  pending,
  used,
  revoked,
  expired;

  /// Parses the server's word, defaulting to [pending] for one this build does not know — an
  /// unrecognised status must not make the row unrenderable.
  static InviteStatus parse(String? raw) => switch (raw) {
    'used' => InviteStatus.used,
    'revoked' => InviteStatus.revoked,
    'expired' => InviteStatus.expired,
    _ => InviteStatus.pending,
  };
}

/// An invitation.
///
/// [code] is present ONLY on the one returned by the request that created it. The server keeps a
/// hash, so a link not copied then cannot be recovered — see api/internal/domain (Invite).
class AdminInvite {
  const AdminInvite({
    required this.id,
    required this.prefix,
    required this.status,
    required this.createdAt,
    this.note,
    this.expiresAt,
    this.usedAt,
    this.usedBy,
    this.code,
  });

  final String id;
  final String prefix;
  final InviteStatus status;
  final String createdAt;
  final String? note;
  final String? expiresAt;
  final String? usedAt;
  final String? usedBy;
  final String? code;

  factory AdminInvite.fromJson(Map<String, dynamic> j) => AdminInvite(
    id: j['id'] as String? ?? '',
    prefix: j['prefix'] as String? ?? '',
    status: InviteStatus.parse(j['status'] as String?),
    createdAt: j['createdAt'] as String? ?? '',
    note: j['note'] as String?,
    expiresAt: j['expiresAt'] as String?,
    usedAt: j['usedAt'] as String?,
    usedBy: j['usedBy'] as String?,
    code: j['code'] as String?,
  );
}

/// A channel and how many messages it has carried, for the overview's leaderboard.
class ChannelVolume {
  const ChannelVolume({
    required this.channelId,
    required this.name,
    required this.count,
  });

  final String channelId;
  final String name;
  final int count;

  factory ChannelVolume.fromJson(Map<String, dynamic> j) => ChannelVolume(
    channelId: j['channelId'] as String? ?? '',
    name: j['name'] as String? ?? '',
    count: (j['count'] as num?)?.toInt() ?? 0,
  );
}

/// The system-wide overview.
class AdminStats {
  const AdminStats({
    required this.users,
    required this.channels,
    required this.messages,
    required this.deliveries,
    required this.devices,
    required this.topChannels,
    required this.recentMessages,
  });

  final int users;
  final int channels;
  final int messages;
  final int deliveries;
  final int devices;
  final List<ChannelVolume> topChannels;
  final List<Message> recentMessages;

  factory AdminStats.fromJson(Map<String, dynamic> j) => AdminStats(
    users: (j['users'] as num?)?.toInt() ?? 0,
    channels: (j['channels'] as num?)?.toInt() ?? 0,
    messages: (j['messages'] as num?)?.toInt() ?? 0,
    deliveries: (j['deliveries'] as num?)?.toInt() ?? 0,
    devices: (j['devices'] as num?)?.toInt() ?? 0,
    topChannels: ((j['topChannels'] as List?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(ChannelVolume.fromJson)
        .toList(growable: false),
    recentMessages: ((j['recentMessages'] as List?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(Message.fromJson)
        .toList(growable: false),
  );
}
