import 'package:dio/dio.dart';

import '../core/api_client.dart';
import '../core/api_exception.dart';
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

  Future<Device> createDevice({required String platform, String? fcmToken}) {
    final body = <String, dynamic>{'platform': platform};
    if (fcmToken != null) body['fcmToken'] = fcmToken;
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

  // --- HTTP helpers ---

  Future<Map<String, dynamic>> _get(
    String path, {
    Map<String, dynamic>? query,
  }) => _send(() => _dio.get<dynamic>(path, queryParameters: query));

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
