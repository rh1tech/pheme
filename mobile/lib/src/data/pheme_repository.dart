import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';

import '../core/api_client.dart';
import '../core/api_exception.dart';
import '../models/chat_models.dart';
import '../models/models.dart';

/// Typed wrapper over the Pheme App API (non-admin surface), mirroring
/// web/src/lib/api.ts. Throws [ApiException] / [AuthException] on failure.
class PhemeRepository {
  PhemeRepository(this._dio);

  final Dio _dio;

  // --- Auth (public) ---

  /// Starts registration: the server emails a 6-digit verification code. No
  /// account exists and no tokens are issued until [verifyEmail] confirms it.
  Future<void> register(String email, String password) => _post(
    '/v1/auth/register',
    {'email': email, 'password': password},
    public: true,
  );

  /// Confirms a pending signup's code, creating the account and logging in.
  Future<TokenResponse> verifyEmail(String email, String code) => _post(
    '/v1/auth/verify',
    {'email': email, 'code': code},
    public: true,
  ).then((d) => TokenResponse.fromJson(d));

  Future<TokenResponse> login(String email, String password) => _post(
    '/v1/auth/login',
    {'email': email, 'password': password},
    public: true,
  ).then((d) => TokenResponse.fromJson(d));

  /// Exchanges a refresh token for a new pair.
  ///
  /// Dio already refreshes transparently on a 401, but that only helps a request that gets a reply.
  /// The SSE stream is closed by the server the moment its token expires, and reconnecting with the
  /// same dead token just gets closed again — so it has to refresh *before* it dials, which means
  /// asking for it explicitly. Refreshing is a plain JWT re-issue with no server-side revocation
  /// list, so this racing with the interceptor's own refresh is harmless: both simply succeed.
  Future<TokenResponse> refreshSession(String refreshToken) => _post(
    '/v1/auth/refresh',
    {'refreshToken': refreshToken},
    public: true,
  ).then((d) => TokenResponse.fromJson(d));

  /// Requests a password-reset code by email. Always succeeds (the server does
  /// not reveal whether the address is registered).
  Future<void> forgotPassword(String email) =>
      _post('/v1/auth/forgot-password', {'email': email}, public: true);

  /// Confirms a reset code, sets the new password, and logs in.
  Future<TokenResponse> resetPassword(
    String email,
    String code,
    String newPassword,
  ) => _post('/v1/auth/reset-password', {
    'email': email,
    'code': code,
    'newPassword': newPassword,
  }, public: true).then((d) => TokenResponse.fromJson(d));

  // --- Profile (self) ---

  /// The authenticated user's own account and profile.
  Future<User> getMe() => _get('/v1/me').then((d) => User.fromJson(d));

  /// Updates the caller's username and contact fields. An empty [username]
  /// clears it. The server returns 409 when the username is taken.
  Future<User> updateMe({
    String? username,
    String? displayName,
    String? bio,
    String? phone,
    String? website,
  }) {
    final body = <String, dynamic>{};
    if (username != null) body['username'] = username;
    if (displayName != null) body['displayName'] = displayName;
    if (bio != null) body['bio'] = bio;
    if (phone != null) body['phone'] = phone;
    if (website != null) body['website'] = website;
    return _patch('/v1/me', body).then((d) => User.fromJson(d));
  }

  /// Uploads a new avatar (processed and stored server-side) and returns the
  /// updated user.
  Future<User> uploadAvatar(String imagePath) {
    final form = FormData.fromMap({
      'avatar': MultipartFile.fromFileSync(
        imagePath,
        filename: imagePath.split('/').last,
      ),
    });
    return _post('/v1/me/avatar', form).then((d) => User.fromJson(d));
  }

  /// Removes the caller's avatar.
  Future<User> deleteAvatar() =>
      _delete('/v1/me/avatar').then((d) => User.fromJson(d));

  // --- Channels ---

  Future<List<Channel>> listChannels() async {
    final data = await _get('/v1/channels');
    final list = (data['channels'] as List?) ?? const [];
    return list
        .map((e) => Channel.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  Future<Channel> createChannel(String name, SubscriptionMode mode) => _post(
    '/v1/channels',
    {'name': name, 'subscriptionMode': mode.wire},
  ).then((d) => Channel.fromJson(d));

  Future<Channel> updateChannel(
    String id, {
    String? name,
    SubscriptionMode? mode,
    String? alias,
  }) {
    final body = <String, dynamic>{};
    if (name != null) body['name'] = name;
    if (mode != null) body['subscriptionMode'] = mode.wire;
    if (alias != null) body['alias'] = alias;
    return _patch('/v1/channels/$id', body).then((d) => Channel.fromJson(d));
  }

  Future<void> deleteChannel(String id) => _delete('/v1/channels/$id');

  // --- Membership ---

  /// The caller's relationship to a single channel. The server returns 404 when
  /// the caller is neither the owner nor a member.
  Future<ChannelRelation> getChannel(String id) async {
    final data = await _get('/v1/channels/$id');
    return ChannelRelation.fromJson(data);
  }

  /// Joins a channel by [ref] — either its trigger ID (publicId) or its phetag
  /// (alias). Returns the joined channel.
  Future<Channel> joinChannel(String ref, {String? deviceId}) {
    final body = <String, dynamic>{'ref': ref};
    if (deviceId != null) body['deviceId'] = deviceId;
    return _post('/v1/channels/join', body).then(
      (d) => Channel.fromJson((d['channel'] as Map).cast<String, dynamic>()),
    );
  }

  Future<List<JoinedChannel>> listJoinedChannels() async {
    final data = await _get('/v1/channels/joined');
    final list = (data['channels'] as List?) ?? const [];
    return list
        .map((e) => JoinedChannel.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// Leaves a channel the caller has joined. The server returns 400 for owners.
  Future<void> leaveChannel(String id) =>
      _delete('/v1/channels/$id/membership');

  // --- Subscriber management (owner/admin) ---

  Future<List<ChannelMember>> listApprovals(String channelId) async {
    final data = await _get('/v1/channels/$channelId/approvals');
    final list = (data['members'] as List?) ?? const [];
    return list
        .map((e) => ChannelMember.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  Future<void> approveMember(String channelId, String userId) =>
      _post('/v1/channels/$channelId/approvals/$userId', null);

  Future<void> denyMember(String channelId, String userId) =>
      _delete('/v1/channels/$channelId/approvals/$userId');

  Future<({List<ChannelMember> items, int total})> listMembers(
    String channelId, {
    int offset = 0,
    int limit = 50,
  }) async {
    final data = await _get(
      '/v1/channels/$channelId/members',
      query: {'offset': '$offset', 'limit': '$limit'},
    );
    final list = (data['members'] as List?) ?? const [];
    return (
      items: list
          .map(
            (e) => ChannelMember.fromJson((e as Map).cast<String, dynamic>()),
          )
          .toList(),
      total: (data['total'] as num?)?.toInt() ?? 0,
    );
  }

  Future<void> updateMember(
    String channelId,
    String userId, {
    ChannelRole? role,
    MemberStatus? status,
  }) {
    final body = <String, dynamic>{};
    if (role != null) body['role'] = role.wire;
    if (status != null) body['status'] = status.wire;
    return _patch('/v1/channels/$channelId/members/$userId', body);
  }

  Future<void> removeMember(String channelId, String userId) =>
      _delete('/v1/channels/$channelId/members/$userId');

  // --- API keys ---

  Future<CreatedKey> createKey(String channelId) => _post(
    '/v1/channels/$channelId/keys',
    null,
  ).then((d) => CreatedKey.fromJson(d));

  Future<List<ApiKey>> listKeys(String channelId) async {
    final data = await _get('/v1/channels/$channelId/keys');
    final list = (data['keys'] as List?) ?? const [];
    return list
        .map((e) => ApiKey.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  Future<void> revokeKey(String channelId, String keyId) =>
      _delete('/v1/channels/$channelId/keys/$keyId');

  // --- Send (owner) ---

  /// Sends a message. With [imagePaths], the request is multipart/form-data (each
  /// image is processed and stored server-side); text-only sends stay JSON.
  Future<void> notifyChannel(
    String channelId,
    String title,
    String body, {
    List<String> imagePaths = const [],
    bool allowComments = true,
  }) {
    final path = '/v1/channels/$channelId/notify';
    if (imagePaths.isEmpty) {
      return _post(path, {
        'title': title,
        'body': body,
        'commentsAllowed': allowComments,
      });
    }
    final form = FormData.fromMap({
      'title': title,
      'body': body,
      'commentsAllowed': '$allowComments',
      'images': [
        for (final p in imagePaths)
          MultipartFile.fromFileSync(p, filename: p.split('/').last),
      ],
    });
    return _post(path, form);
  }

  /// Absolute URL of a processed message image (served publicly by the App API).
  String imageUrl(String id) => '${_dio.options.baseUrl}/v1/images/$id';

  // --- Devices & subscriptions ---

  /// Registers this device for push.
  ///
  /// [voipToken] is the iOS PushKit token, and it is a DIFFERENT token from [fcmToken] — both are
  /// sent. FCM carries messages; only PushKit can carry a call that rings a sleeping iPhone, and FCM
  /// has no way to reach it.
  Future<Device> createDevice({
    required String platform,
    String? fcmToken,
    String? voipToken,
  }) {
    final body = <String, dynamic>{'platform': platform};
    if (fcmToken != null) body['fcmToken'] = fcmToken;
    if (voipToken != null && voipToken.isNotEmpty) {
      body['voipToken'] = voipToken;
    }
    return _post('/v1/devices', body).then((d) => Device.fromJson(d));
  }

  Future<void> deleteDevice(String id) => _delete('/v1/devices/$id');

  Future<void> subscribe(String channelId, String deviceId) =>
      _post('/v1/channels/$channelId/subscribe', {'deviceId': deviceId});

  Future<void> unsubscribe(String channelId, String deviceId) => _delete(
    '/v1/channels/$channelId/subscribe',
    query: {'deviceId': deviceId},
  );

  Future<SubscriptionStatus> channelSubscription(
    String channelId,
    String deviceId,
  ) async {
    final data = await _get(
      '/v1/channels/$channelId/subscription',
      query: {'deviceId': deviceId},
    );
    return subscriptionStatusFromWire(data['status'] as String?);
  }

  // --- Messages ---

  Future<Message> getMessage(String channelId, String messageId) async {
    final data = await _get('/v1/channels/$channelId/messages/$messageId');
    return Message.fromJson(data);
  }

  Future<MessagesPage> listMessages(
    String channelId, {
    String cursor = '',
    String query = '',
    int limit = 50,
  }) async {
    final q = <String, dynamic>{'limit': '$limit'};
    if (cursor.isNotEmpty) q['cursor'] = cursor;
    if (query.isNotEmpty) q['q'] = query;
    final data = await _get('/v1/channels/$channelId/messages', query: q);
    final list = (data['messages'] as List?) ?? const [];
    return MessagesPage(
      messages: list
          .map((e) => Message.fromJson((e as Map).cast<String, dynamic>()))
          .toList(),
      nextCursor: data['nextCursor'] as String? ?? '',
    );
  }

  // --- Comments ---

  Future<CommentsPage> listComments(
    String channelId,
    String messageId, {
    String cursor = '',
    int limit = 50,
  }) async {
    final q = <String, dynamic>{'limit': '$limit'};
    if (cursor.isNotEmpty) q['cursor'] = cursor;
    final data = await _get(
      '/v1/channels/$channelId/messages/$messageId/comments',
      query: q,
    );
    final list = (data['comments'] as List?) ?? const [];
    return CommentsPage(
      comments: list
          .map((e) => Comment.fromJson((e as Map).cast<String, dynamic>()))
          .toList(),
      nextCursor: data['nextCursor'] as String? ?? '',
    );
  }

  Future<Comment> postComment(
    String channelId,
    String messageId,
    String body,
  ) => _post('/v1/channels/$channelId/messages/$messageId/comments', {
    'body': body,
  }).then((d) => Comment.fromJson(d));

  Future<void> deleteComment(
    String channelId,
    String messageId,
    String commentId,
  ) => _delete(
    '/v1/channels/$channelId/messages/$messageId/comments/$commentId',
  );

  // --- Conversations ---

  /// Opens (or re-opens) the direct chat with [userId]. Idempotent: the server dedupes on the
  /// unordered pair, so calling it twice returns the same conversation rather than a second one.
  Future<Conversation> createDirectChat(String userId) =>
      _post('/v1/conversations', {
        'kind': 'direct',
        'memberIds': [userId],
      }).then((d) => Conversation.fromJson(d));

  /// Creates a group. The caller becomes its admin; everyone else joins as a plain member.
  Future<Conversation> createGroupChat(String title, List<String> memberIds) =>
      _post('/v1/conversations', {
        'kind': 'group',
        'title': title,
        'memberIds': memberIds,
      }).then((d) => Conversation.fromJson(d));

  Future<List<Conversation>> listConversations() =>
      _get('/v1/conversations').then(
        (d) => ((d['conversations'] as List?) ?? const [])
            .map(
              (e) => Conversation.fromJson((e as Map).cast<String, dynamic>()),
            )
            .toList(),
      );

  Future<Conversation> getConversation(String id) =>
      _get('/v1/conversations/$id').then((d) => Conversation.fromJson(d));

  /// Direct: either party may delete. Group: admins only.
  Future<void> deleteConversation(String id) =>
      _delete('/v1/conversations/$id');

  /// Clears the caller's own history of a conversation, keeping the conversation. The
  /// server sets a per-member watermark rather than deleting the shared message log, so
  /// it hides this user's history on all their devices without touching anyone else's.
  Future<void> clearChatHistory(String id) =>
      _delete('/v1/conversations/$id/messages');

  /// Reports how far this user has got in a conversation, so the sender's ticks can fill in.
  ///
  /// Watermarks, not message ids — "I have read up to this instant". Both only ever move forward
  /// server-side, so a duplicate or out-of-order report is harmless.
  Future<void> reportReceipt(String id, {String? delivered, String? read}) =>
      _post('/v1/conversations/$id/receipts', {
        'delivered': ?delivered,
        'read': ?read,
      });

  /// One page of history, newest-first, walking backwards. [cursor] is the previous page's
  /// [ChatMessagesPage.nextCursor].
  Future<ChatMessagesPage> listChatMessages(
    String id, {
    String? cursor,
    int limit = 50,
  }) async {
    final d = await _get(
      '/v1/conversations/$id/messages',
      query: {'limit': limit, 'cursor': ?cursor},
    );
    final next = d['nextCursor'] as String?;
    return ChatMessagesPage(
      messages: ((d['messages'] as List?) ?? const [])
          .map((e) => ChatMessage.fromJson((e as Map).cast<String, dynamic>()))
          .toList(),
      // The server only sends a cursor when the page was full; an empty string means "no more".
      nextCursor: (next == null || next.isEmpty) ? null : next,
    );
  }

  /// Posts an encrypted message.
  ///
  /// The server rejects MLS Welcomes and Commits here — those go to [mlsCommit], because they have
  /// to be ordered against an epoch and an ordinary message does not.
  Future<ChatMessage> sendChatMessage(
    String id,
    Uint8List ciphertext,
    String contentType,
  ) => _post('/v1/conversations/$id/messages', {
    'ciphertext': base64Encode(ciphertext),
    'contentType': contentType,
  }).then((d) => ChatMessage.fromJson(d));

  /// Uploads one encrypted photo and returns its blob id.
  ///
  /// The body is raw ciphertext, not JSON and not multipart: the server stores it as opaque bytes and
  /// must not be handed a filename or a content type it has no business knowing. What it can open, it
  /// cannot — the key travels inside the MLS-encrypted message that references this id.
  Future<String> uploadAttachment(
    String conversationId,
    Uint8List sealed,
  ) async {
    final res = await _send(
      () => _dio.post<dynamic>(
        '/v1/conversations/$conversationId/attachments',
        data: Stream.fromIterable([sealed]),
        options: Options(
          headers: {
            'Content-Type': 'application/octet-stream',
            'Content-Length': sealed.length,
          },
        ),
      ),
    );
    return res['id'] as String? ?? '';
  }

  /// Uploads a sealed transcript blob for a newly-joined device (history sync), returning its id.
  /// Opaque ciphertext — sealed under a group-derived key the server never has.
  Future<String> uploadHistory(String conversationId, Uint8List sealed) async {
    final res = await _send(
      () => _dio.post<dynamic>(
        '/v1/conversations/$conversationId/mls/history',
        data: Stream.fromIterable([sealed]),
        options: Options(
          headers: {
            'Content-Type': 'application/octet-stream',
            'Content-Length': sealed.length,
          },
        ),
      ),
    );
    return res['id'] as String? ?? '';
  }

  /// Fetches a sealed transcript blob once — the server deletes it after (one-shot). Still ciphertext.
  Future<Uint8List> getHistory(String conversationId, String historyId) async {
    try {
      final res = await _dio.get<List<int>>(
        '/v1/conversations/$conversationId/mls/history/$historyId',
        options: Options(responseType: ResponseType.bytes),
      );
      return Uint8List.fromList(res.data ?? const []);
    } on DioException catch (e) {
      final err = e.error;
      if (err is ApiException || err is AuthException) throw err as Object;
      throw ApiException(0, e.message ?? 'network error');
    }
  }

  /// Fetches one encrypted photo. Still ciphertext — the caller opens it with the key from the message.
  Future<Uint8List> downloadAttachment(
    String conversationId,
    String attachmentId,
  ) async {
    try {
      final res = await _dio.get<List<int>>(
        '/v1/conversations/$conversationId/attachments/$attachmentId',
        options: Options(responseType: ResponseType.bytes),
      );
      return Uint8List.fromList(res.data ?? const []);
    } on DioException catch (e) {
      final err = e.error;
      if (err is ApiException || err is AuthException) throw err as Object;
      throw ApiException(0, e.message ?? 'network error');
    }
  }

  // --- Group membership ---

  Future<List<ConversationMember>> listConversationMembers(String id) =>
      _get('/v1/conversations/$id/members').then(
        (d) => ((d['members'] as List?) ?? const [])
            .map(
              (e) => ConversationMember.fromJson(
                (e as Map).cast<String, dynamic>(),
              ),
            )
            .toList(),
      );

  /// Adds a member. Groups only, admins only.
  Future<ConversationMember> addConversationMember(String id, String userId) =>
      _post('/v1/conversations/$id/members', {
        'userId': userId,
      }).then((d) => ConversationMember.fromJson(d));

  Future<void> setConversationMemberRole(
    String id,
    String userId,
    ChannelRole role,
  ) => _patch('/v1/conversations/$id/members/$userId', {'role': role.wire});

  /// Removes a member — or, when [userId] is the caller, leaves.
  Future<void> removeConversationMember(String id, String userId) =>
      _delete('/v1/conversations/$id/members/$userId');

  /// Searches public profiles. The server requires at least two characters and never matches on
  /// email.
  Future<List<PublicUser>> searchUsers(String q) =>
      _get('/v1/users/search', query: {'q': q}).then(
        (d) => ((d['users'] as List?) ?? const [])
            .map((e) => PublicUser.fromJson((e as Map).cast<String, dynamic>()))
            .toList(),
      );

  // --- MLS key material ---

  /// Publishes this device's KeyPackages, so other members can add it to a group. The optional
  /// [label] (e.g. "Pheme on iPhone") is recorded in the user's device registry so they can
  /// recognise this device in "your devices".
  Future<void> publishKeyPackages(
    String deviceId,
    List<Uint8List> keyPackages, {
    Uint8List? lastResortKeyPackage,
    String? label,
  }) => _post('/v1/mls/key-packages', {
    'deviceId': deviceId,
    'keyPackages': keyPackages.map(base64Encode).toList(),
    if (lastResortKeyPackage != null)
      'lastResortKeyPackage': base64Encode(lastResortKeyPackage),
    if (label != null && label.isNotEmpty) 'label': label,
  });

  /// The signed-in user's own devices — for the "your devices" panel. Newest activity first.
  Future<List<MLSDevice>> myDevices() async {
    final d = await _get('/v1/mls/devices');
    return ((d['devices'] as List?) ?? const [])
        .map((e) => MLSDevice.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// Terminates one of the caller's own devices server-side: deletes its key packages so it cannot
  /// be re-added, revokes its login, and forgets it from the registry. The MLS leaf removal is done
  /// first, client-side, by MlsService.terminateOwnDevice.
  Future<void> terminateDevice(String deviceId) =>
      _delete('/v1/mls/devices/$deviceId');

  /// How much single-use stock this device has left, so we know when to replenish.
  Future<MLSKeyPackageCount> keyPackageCount(String deviceId) => _get(
    '/v1/mls/key-packages/count',
    query: {'deviceId': deviceId},
  ).then((d) => MLSKeyPackageCount.fromJson(d));

  /// Purges a retired device's key packages, so nobody claims one it can no longer open.
  Future<void> deleteKeyPackages(String deviceId) =>
      _delete('/v1/mls/key-packages', query: {'deviceId': deviceId});

  /// Every device every member has published, as `userId -> [deviceId]`. Consumes nothing — this is
  /// the directory reconciliation diffs against.
  Future<Map<String, List<String>>> mlsDevices(String conversationId) async {
    final d = await _get('/v1/conversations/$conversationId/mls/devices');
    final devices = (d['devices'] as Map?) ?? const {};
    return devices.map(
      (k, v) => MapEntry(
        k as String,
        ((v as List?) ?? const []).map((e) => e as String).toList(),
      ),
    );
  }

  /// Claims one KeyPackage per named device. Each is single-use, so this consumes stock.
  ///
  /// Throws a 404 [ApiException] when none of the devices has a usable package left — which means
  /// the peer cannot be added, not that the request was malformed.
  Future<List<MLSClaimedKeyPackage>> claimKeyPackages(
    String conversationId,
    List<MLSDeviceRef> devices,
  ) =>
      _post('/v1/conversations/$conversationId/mls/key-packages/claim', {
        'devices': devices.map((d) => d.toJson()).toList(),
      }).then(
        (d) => ((d['keyPackages'] as List?) ?? const [])
            .map(
              (e) => MLSClaimedKeyPackage.fromJson(
                (e as Map).cast<String, dynamic>(),
              ),
            )
            .toList(),
      );

  Future<MLSGroupState> mlsGroupState(String conversationId) => _get(
    '/v1/conversations/$conversationId/mls',
  ).then((d) => MLSGroupState.fromJson(d));

  /// The latest GroupInfo a new device can external-join against, or null when none has been published
  /// for the current group (404) — in which case the caller falls back to announcing itself.
  Future<({String groupId, int epoch, Uint8List groupInfo})?> mlsGroupInfo(
    String conversationId,
  ) async {
    try {
      final d = await _get('/v1/conversations/$conversationId/mls/group-info');
      return (
        groupId: d['groupId'] as String,
        epoch: (d['epoch'] as num).toInt(),
        groupInfo: base64Decode(d['groupInfo'] as String),
      );
    } on ApiException catch (e) {
      if (e.statusCode == 404) return null;
      rethrow;
    }
  }

  /// Publishes the GroupInfo a member exported after a Commit, so a future joiner can external-join.
  /// Best effort: a stale or missing one only costs a joiner a fall back to announcing itself.
  Future<void> publishGroupInfo(
    String conversationId, {
    required String groupId,
    required int epoch,
    required Uint8List groupInfo,
  }) => _post('/v1/conversations/$conversationId/mls/group-info', {
    'groupId': groupId,
    'epoch': epoch,
    'groupInfo': base64Encode(groupInfo),
  });

  /// Every Welcome and Commit after [since], oldest-first, so a device can catch up in order.
  Future<List<ChatMessage>> mlsCommitsSince(String conversationId, int since) =>
      _get(
        '/v1/conversations/$conversationId/mls/commits',
        query: {'since': since},
      ).then(
        (d) => ((d['messages'] as List?) ?? const [])
            .map(
              (e) => ChatMessage.fromJson((e as Map).cast<String, dynamic>()),
            )
            .toList(),
      );

  /// Offers a Commit as the group's next epoch. A compare-and-set on [baseEpoch].
  ///
  /// This is the one call whose failure is not an error: two members can stage a Commit at the same
  /// epoch and only one can win. The loser gets [MlsCommitResult.accepted] == false along with the
  /// state that did win, and must discard its own Commit (never apply it), catch up, and retry.
  Future<MlsCommitResult> mlsCommit(
    String conversationId, {
    required String groupId,
    required int baseEpoch,
    required Uint8List commit,
    Uint8List? welcome,
    List<String> removes = const [],
  }) async {
    try {
      final d = await _post('/v1/conversations/$conversationId/mls/commit', {
        'groupId': groupId,
        'baseEpoch': baseEpoch,
        'commit': base64Encode(commit),
        if (welcome != null) 'welcome': base64Encode(welcome),
        if (removes.isNotEmpty) 'removes': removes,
      });
      return MlsCommitResult(accepted: true, state: MLSGroupState.fromJson(d));
    } on DioException catch (e) {
      final body = e.response?.data;
      if (e.response?.statusCode == 409 && body is Map) {
        return MlsCommitResult(
          accepted: false,
          state: MLSGroupState.fromJson(body.cast<String, dynamic>()),
        );
      }
      rethrow;
    }
  }

  /// Retires the current group and starts a fresh one, for the case where a device could never be
  /// admitted. The old group is remembered in [MLSGroupState.priorGroupIds] — nothing already sent
  /// becomes unreadable.
  Future<MLSGroupState> mlsResetGroup(String conversationId) => _post(
    '/v1/conversations/$conversationId/mls/reset',
    const {},
  ).then((d) => MLSGroupState.fromJson(d));

  /// Stores the passphrase-sealed client state. The server can read none of it.
  Future<void> putKeyBackup(
    String deviceId, {
    required Uint8List salt,
    required Uint8List nonce,
    required Uint8List ciphertext,
    Uint8List? transcriptSalt,
    Uint8List? transcriptNonce,
    Uint8List? transcriptCiphertext,
  }) {
    final body = <String, dynamic>{
      'deviceId': deviceId,
      'salt': base64Encode(salt),
      'nonce': base64Encode(nonce),
      'ciphertext': base64Encode(ciphertext),
    };
    // The transcript seal travels whole or not at all — a ciphertext with no salt/nonce could
    // never be opened, and the server rejects a partial one.
    if (transcriptCiphertext != null &&
        transcriptSalt != null &&
        transcriptNonce != null) {
      body['transcriptSalt'] = base64Encode(transcriptSalt);
      body['transcriptNonce'] = base64Encode(transcriptNonce);
      body['transcriptCiphertext'] = base64Encode(transcriptCiphertext);
    }
    return _put('/v1/mls/key-backup', body);
  }

  /// The sealed backup, or null when there is none.
  Future<MLSKeyBackup?> getKeyBackup() async {
    try {
      return MLSKeyBackup.fromJson(await _get('/v1/mls/key-backup'));
    } on ApiException catch (e) {
      if (e.statusCode == 404) return null;
      rethrow;
    }
  }

  // --- Calls ---

  /// The STUN/TURN servers for this call. Fetched per call, never cached: the credentials expire.
  ///
  /// Throws [CallingUnavailableException] when the server has no TURN configured — which is how the
  /// UI knows not to offer a call button at all.
  Future<List<IceServer>> iceServers() async {
    try {
      final d = await _get('/v1/calls/ice-servers');
      return ((d['iceServers'] as List?) ?? const [])
          .map((e) => IceServer.fromJson((e as Map).cast<String, dynamic>()))
          .toList();
    } on ApiException catch (e) {
      if (e.statusCode == 503) throw CallingUnavailableException();
      rethrow;
    }
  }

  /// Appends a sealed signal to the call's mailbox and returns its sequence number.
  ///
  /// [ring] is set only on the invite: it is what fans a push out to the callee's devices. [cancel]
  /// is set when giving up before an answer, and takes the ringing notification back off their lock
  /// screen — without it a dead call sits there looking live.
  Future<int> callSignal(
    String conversationId,
    String callId,
    Uint8List ciphertext, {
    bool ring = false,
    bool cancel = false,
  }) => _post('/v1/conversations/$conversationId/calls/$callId/signal', {
    'ciphertext': base64Encode(ciphertext),
    if (ring) 'ring': true,
    if (cancel) 'cancel': true,
  }).then((d) => (d['seq'] as num?)?.toInt() ?? 0);

  /// Reads the call's mailbox from a cursor. This — not the live stream — is the transport of
  /// record: the stream may drop a nudge, and a dropped SDP answer is a call that never connects.
  Future<List<CallSignal>> callSignals(
    String conversationId,
    String callId, {
    int since = 0,
  }) =>
      _get(
        '/v1/conversations/$conversationId/calls/$callId/signals',
        query: {'since': since},
      ).then(
        (d) => ((d['signals'] as List?) ?? const [])
            .map((e) => CallSignal.fromJson((e as Map).cast<String, dynamic>()))
            .toList(),
      );

  /// Claims the call for this device. The server-side answer lock.
  ///
  /// Returns true if we won. False means another of our own devices picked up first, and this one
  /// must stop ringing and let go of the microphone. That verdict cannot ride the live stream —
  /// the stream is allowed to drop events, and a device holding an open mic cannot be left guessing.
  Future<bool> callAccept(
    String conversationId,
    String callId,
    String deviceId,
  ) async {
    try {
      await _post('/v1/conversations/$conversationId/calls/$callId/accept', {
        'deviceId': deviceId,
      });
      return true;
    } on ApiException catch (e) {
      if (e.statusCode == 409) return false;
      rethrow;
    }
  }

  /// Re-nudges an unanswered invite over the live stream. Deliberately sends no push: re-buzzing
  /// somebody's phone every three seconds is harassment.
  Future<void> callRing(String conversationId, String callId) =>
      _post('/v1/conversations/$conversationId/calls/$callId/ring', const {});

  // --- HTTP helpers ---

  Future<Map<String, dynamic>> _get(
    String path, {
    Map<String, dynamic>? query,
  }) => _send(() => _dio.get<dynamic>(path, queryParameters: query));

  Future<Map<String, dynamic>> _put(String path, Object? body) =>
      _send(() => _dio.put<dynamic>(path, data: body));

  Future<Map<String, dynamic>> _post(
    String path,
    Object? body, {
    bool public = false,
  }) => _send(
    () => _dio.post<dynamic>(
      path,
      data: body,
      options: Options(extra: public ? publicRequest : null),
    ),
  );

  Future<Map<String, dynamic>> _patch(String path, Object? body) =>
      _send(() => _dio.patch<dynamic>(path, data: body));

  Future<Map<String, dynamic>> _delete(
    String path, {
    Map<String, dynamic>? query,
  }) => _send(() => _dio.delete<dynamic>(path, queryParameters: query));

  Future<Map<String, dynamic>> _send(
    Future<Response<dynamic>> Function() call,
  ) async {
    try {
      final res = await call();
      final data = res.data;
      if (data is Map) return data.cast<String, dynamic>();
      return const {};
    } on DioException catch (e) {
      final err = e.error;
      if (err is ApiException || err is AuthException) throw err as Object;
      throw ApiException(0, e.message ?? 'network error');
    }
  }
}
