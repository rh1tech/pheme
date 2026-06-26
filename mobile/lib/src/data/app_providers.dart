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
