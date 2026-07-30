// The admin API, typed.
//
// A separate class from PhemeRepository rather than more methods on it, for the same reason
// admin_models.dart is a separate file: this is the surface that answers 403 to almost every
// account on the server, and an ordinary screen should not be able to reach it by accident.
//
// It shares the same Dio — the same base URL, the same bearer token, the same refresh-on-401
// interceptor — because "admin" here is a claim inside the caller's ordinary session, not a
// second credential.

import 'package:dio/dio.dart';

import '../core/api_exception.dart';
import '../models/models.dart';
import 'admin_models.dart';

class AdminRepository {
  AdminRepository(this._dio);

  final Dio _dio;

  // --- Overview ---

  Future<AdminStats> stats() =>
      _get('/v1/admin/stats').then(AdminStats.fromJson);

  // --- Users ---

  Future<AdminPage<AdminUser>> listUsers({
    String query = '',
    int page = 1,
    int limit = 20,
  }) => _get(
    '/v1/admin/users',
    query: _pageQuery(query, page, limit),
  ).then((d) => _page(d, 'users', AdminUser.fromJson, page, limit));

  /// Creates an account directly, with no email verification and no invitation.
  ///
  /// This is the escape hatch that keeps an invite-only server administrable: it is how the first
  /// accounts exist, and how an admin adds somebody without waiting on mail delivery.
  Future<AdminUser> createUser({
    required String email,
    required String password,
    required String role,
  }) => _post('/v1/admin/users', {
    'email': email,
    'password': password,
    'role': role,
  }).then(AdminUser.fromJson);

  /// Changes a user's role, their status, or both.
  ///
  /// A null field is OMITTED, not sent as null: the server treats an absent field as "leave it
  /// alone", so sending both every time would make every role change also rewrite the status.
  Future<void> updateUser(String userId, {String? role, String? status}) {
    final body = <String, dynamic>{};
    if (role != null) body['role'] = role;
    if (status != null) body['status'] = status;
    return _patch('/v1/admin/users/$userId', body);
  }

  Future<void> resetUserPassword(String userId, String newPassword) => _post(
    '/v1/admin/users/$userId/reset-password',
    {'newPassword': newPassword},
  );

  /// Deletes the account AND everything it owns — channels, keys, subscriptions, messages.
  Future<void> deleteUser(String userId) => _delete('/v1/admin/users/$userId');

  // --- Channels ---

  Future<AdminPage<AdminChannel>> listChannels({
    String query = '',
    int page = 1,
    int limit = 20,
  }) => _get(
    '/v1/admin/channels',
    query: _pageQuery(query, page, limit),
  ).then((d) => _page(d, 'channels', AdminChannel.fromJson, page, limit));

  /// Sets a channel active or disabled. A disabled channel takes no new messages.
  Future<void> setChannelStatus(String channelId, ChannelStatus status) =>
      _patch('/v1/admin/channels/$channelId', {
        'status': status == ChannelStatus.disabled ? 'disabled' : 'active',
      });

  Future<void> deleteChannel(String channelId) =>
      _delete('/v1/admin/channels/$channelId');

  Future<MessagesPage> channelMessages(
    String channelId, {
    String cursor = '',
    String query = '',
    int limit = 50,
  }) =>
      _get(
        '/v1/admin/channels/$channelId/messages',
        query: {
          if (cursor.isNotEmpty) 'cursor': cursor,
          if (query.isNotEmpty) 'q': query,
          'limit': limit,
        },
      ).then(
        (d) => MessagesPage(
          messages: ((d['messages'] as List?) ?? const [])
              .cast<Map<String, dynamic>>()
              .map(Message.fromJson)
              .toList(growable: false),
          nextCursor: d['nextCursor'] as String? ?? '',
        ),
      );

  Future<List<ApiKey>> channelKeys(String channelId) =>
      _get('/v1/admin/channels/$channelId/keys').then(
        (d) => ((d['keys'] as List?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(ApiKey.fromJson)
            .toList(growable: false),
      );

  Future<void> revokeChannelKey(String channelId, String keyId) =>
      _delete('/v1/admin/channels/$channelId/keys/$keyId');

  // --- Comments ---

  Future<AdminPage<AdminComment>> listComments({
    String query = '',
    int page = 1,
    int limit = 20,
  }) => _get(
    '/v1/admin/comments',
    query: _pageQuery(query, page, limit),
  ).then((d) => _page(d, 'comments', AdminComment.fromJson, page, limit));

  Future<void> deleteComment(String commentId) =>
      _delete('/v1/admin/comments/$commentId');

  // --- Invitations ---

  Future<AdminPage<AdminInvite>> listInvites({
    String query = '',
    int page = 1,
    int limit = 20,
  }) => _get(
    '/v1/admin/invites',
    query: _pageQuery(query, page, limit),
  ).then((d) => _page(d, 'invites', AdminInvite.fromJson, page, limit));

  /// Mints one invitation. The returned [AdminInvite.code] is the ONLY copy there will ever be —
  /// the server stores a hash — so a caller that does not show it to somebody has thrown it away.
  Future<AdminInvite> createInvite({String note = '', int expiresInDays = 0}) =>
      _post('/v1/admin/invites', {
        'note': note,
        'expiresInDays': expiresInDays,
      }).then(AdminInvite.fromJson);

  Future<void> revokeInvite(String inviteId) =>
      _post('/v1/admin/invites/$inviteId/revoke', null);

  // --- Plumbing ---

  Map<String, dynamic> _pageQuery(String query, int page, int limit) => {
    if (query.isNotEmpty) 'q': query,
    'page': page,
    'limit': limit,
  };

  /// Unwraps one of the admin listings, all of which share a shape: a named array, a total, and
  /// the page coordinates echoed back.
  ///
  /// [page] and [limit] fall back to what was ASKED for when the server does not echo them, so the
  /// pager keeps working against a response that omits them rather than snapping back to page 1.
  AdminPage<T> _page<T>(
    Map<String, dynamic> d,
    String key,
    T Function(Map<String, dynamic>) fromJson,
    int page,
    int limit,
  ) => AdminPage<T>(
    items: ((d[key] as List?) ?? const [])
        .cast<Map<String, dynamic>>()
        .map(fromJson)
        .toList(growable: false),
    total: (d['total'] as num?)?.toInt() ?? 0,
    page: (d['page'] as num?)?.toInt() ?? page,
    limit: (d['limit'] as num?)?.toInt() ?? limit,
  );

  Future<Map<String, dynamic>> _get(
    String path, {
    Map<String, dynamic>? query,
  }) => _send(() => _dio.get<dynamic>(path, queryParameters: query));

  Future<Map<String, dynamic>> _post(String path, Object? body) =>
      _send(() => _dio.post<dynamic>(path, data: body));

  Future<Map<String, dynamic>> _patch(String path, Object? body) =>
      _send(() => _dio.patch<dynamic>(path, data: body));

  Future<Map<String, dynamic>> _delete(String path) =>
      _send(() => _dio.delete<dynamic>(path));

  Future<Map<String, dynamic>> _send(
    Future<Response<dynamic>> Function() call,
  ) async {
    try {
      final res = await call();
      final data = res.data;
      if (data is Map) return data.cast<String, dynamic>();
      // 204, or a body that is not an object. Both are successes with nothing to read.
      return const {};
    } on DioException catch (e) {
      final err = e.error;
      if (err is ApiException || err is AuthException) throw err as Object;
      throw ApiException(0, e.message ?? 'network error');
    }
  }
}
