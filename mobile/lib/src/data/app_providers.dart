import 'package:collection/collection.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../live/sse_client.dart';
import '../models/models.dart';

/// Live message stream (SSE). Active only while authenticated; rebuilds when the
/// base URL changes. Auto-disposes when no screen is listening.
final liveEventsProvider = StreamProvider.autoDispose<LiveEvent>((ref) {
  final auth = ref.watch(authControllerProvider);
  if (!auth.isAuthenticated) return const Stream<LiveEvent>.empty();

  final baseUrl = ref.watch(
    settingsControllerProvider.select((s) => s.baseUrl),
  );
  final tokenStore = ref.watch(tokenStoreProvider);

  final client = SseClient(
    baseUrl: baseUrl,
    getToken: () async => tokenStore.current?.accessToken,
  );
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
