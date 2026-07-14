import 'package:collection/collection.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/jwt.dart';
import '../core/providers.dart';
import '../core/token_store.dart';
import '../live/sse_client.dart';
import '../models/models.dart';

/// Refresh the access token before opening the stream if it has less than this left. The server
/// kills the stream when the token expires, so opening one with a nearly-dead token buys a
/// connection that is about to be cut.
const _streamTokenFloor = Duration(minutes: 2);

/// The live event stream (SSE). Active only while authenticated; rebuilds when the base URL changes.
///
/// Deliberately NOT autoDispose, unlike everything else here: an incoming call has to ring whether
/// or not a screen happens to be listening. If this stream only existed while some widget watched
/// it, the phone would go quiet exactly when the app was idle.
final liveEventsProvider = StreamProvider<LiveEvent>((ref) {
  final auth = ref.watch(authControllerProvider);
  if (!auth.isAuthenticated) return const Stream<LiveEvent>.empty();

  final baseUrl = ref.watch(
    settingsControllerProvider.select((s) => s.baseUrl),
  );
  final tokenStore = ref.watch(tokenStoreProvider);
  final repo = ref.watch(repositoryProvider);

  Future<String?> freshToken() async {
    final tokens = tokenStore.current;
    if (tokens == null) return null;

    final expiry = decodeExpiry(tokens.accessToken);
    final soon = DateTime.now().toUtc().add(_streamTokenFloor);
    if (expiry != null && expiry.isAfter(soon)) return tokens.accessToken;

    try {
      final refreshed = await repo.refreshSession(tokens.refreshToken);
      await tokenStore.save(
        Tokens(
          accessToken: refreshed.accessToken,
          refreshToken: refreshed.refreshToken,
        ),
      );
      return refreshed.accessToken;
    } on Object {
      // Refresh failed — hand back what we have. The connection will be refused and the loop will
      // back off and try again; if the session is truly gone, the next API call drops us to login.
      return tokens.accessToken;
    }
  }

  final client = SseClient(baseUrl: baseUrl, freshToken: freshToken);
  client.start();
  ref.onDispose(client.close);
  return client.events;
});

/// Loads and mutates the authenticated user's channels.
class ChannelsController extends AsyncNotifier<List<Channel>> {
  @override
  Future<List<Channel>> build() => ref.read(repositoryProvider).listChannels();

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(
      () => ref.read(repositoryProvider).listChannels(),
    );
  }

  Future<Channel> create(String name, SubscriptionMode mode) async {
    final channel = await ref
        .read(repositoryProvider)
        .createChannel(name, mode);
    await refresh();
    return channel;
  }
}

final channelsProvider =
    AsyncNotifierProvider<ChannelsController, List<Channel>>(
      ChannelsController.new,
    );

/// Convenience lookup of a single channel from the loaded list.
final channelProvider = Provider.family<Channel?, String>((ref, id) {
  return ref
      .watch(channelsProvider)
      .maybeWhen(
        data: (list) => list.where((c) => c.id == id).firstOrNull,
        orElse: () => null,
      );
});

/// Loads and mutates the channels the user has joined (does not own).
class JoinedChannelsController extends AsyncNotifier<List<JoinedChannel>> {
  @override
  Future<List<JoinedChannel>> build() =>
      ref.read(repositoryProvider).listJoinedChannels();

  Future<void> refresh() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(
      () => ref.read(repositoryProvider).listJoinedChannels(),
    );
  }

  /// Joins a channel by reference (trigger ID or phetag) and refreshes the list.
  Future<Channel> join(String reference, {String? deviceId}) async {
    final channel = await ref
        .read(repositoryProvider)
        .joinChannel(reference, deviceId: deviceId);
    await refresh();
    return channel;
  }
}

final joinedChannelsProvider =
    AsyncNotifierProvider<JoinedChannelsController, List<JoinedChannel>>(
      JoinedChannelsController.new,
    );

/// The caller's relationship to a single channel (owner/role/status). Loaded
/// from the server so it works for owned and joined channels alike, and on cold
/// deep-links where the channel lists aren't populated yet.
final channelRelationProvider = FutureProvider.family<ChannelRelation, String>((
  ref,
  id,
) async {
  return ref.watch(repositoryProvider).getChannel(id);
});
