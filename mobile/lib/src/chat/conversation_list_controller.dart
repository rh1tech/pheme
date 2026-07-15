// The conversation list, and the unread state that goes with it.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../data/app_providers.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import 'chat_providers.dart';

class ConversationListController extends AsyncNotifier<List<Conversation>> {
  @override
  Future<List<Conversation>> build() async {
    // Patch the list from the live stream rather than refetching it. A new message must move a
    // conversation to the top of the list whether or not that chat is open.
    ref.listen(liveEventsProvider, (_, next) {
      final event = next.value;
      if (event == null) return;
      _apply(event);
    });

    final conversations = await ref
        .read(repositoryProvider)
        .listConversations();
    return _sorted(conversations);
  }

  void _apply(LiveEvent event) {
    final conversationId = event.conversationId;
    if (conversationId == null) return;

    final current = state.value;
    if (current == null) return;

    if (event.conversationDeleted) {
      state = AsyncData(
        current.where((c) => c.id != conversationId).toList(growable: false),
      );
      ref.read(chatCacheProvider).forget(conversationId);
      ref.read(chatEnvelopeCacheProvider).forget(conversationId);
      ref.read(lastSeenStoreProvider).forget(conversationId);
      return;
    }

    final message = event.chatMessage;
    if (message == null) return;

    // Control traffic is not a message. It must not bump a conversation up the list, and it must
    // certainly not light up an unread dot — the user has nothing to read.
    if (message.isControl) return;

    final index = current.indexWhere((c) => c.id == conversationId);
    if (index == -1) {
      // A conversation we have never seen — somebody just added us to a group. Only a refetch can
      // tell us who is in it.
      refresh();
      return;
    }

    final updated = [...current];
    final existing = updated.removeAt(index);
    updated.insert(
      0,
      Conversation(
        id: existing.id,
        kind: existing.kind,
        createdBy: existing.createdBy,
        createdAt: existing.createdAt,
        title: existing.title,
        avatarId: existing.avatarId,
        members: existing.members,
        lastMessage: LastChatMessage(
          id: message.id,
          senderId: message.senderId,
          ciphertext: message.ciphertext,
          contentType: message.contentType,
          createdAt: message.createdAt,
        ),
      ),
    );
    state = AsyncData(updated);
  }

  Future<void> refresh() async {
    state = await AsyncValue.guard(() async {
      final conversations = await ref
          .read(repositoryProvider)
          .listConversations();
      return _sorted(conversations);
    });
  }

  /// Opens (or re-opens) the direct chat with [userId]. The server dedupes on the pair, so this is
  /// idempotent — calling it on an existing chat returns that chat rather than a second one.
  Future<Conversation> startDirect(String userId) async {
    final conversation = await ref
        .read(repositoryProvider)
        .createDirectChat(userId);
    await refresh();
    return conversation;
  }

  Future<Conversation> createGroup(String title, List<String> memberIds) async {
    final conversation = await ref
        .read(repositoryProvider)
        .createGroupChat(title, memberIds);
    await refresh();
    return conversation;
  }

  Future<void> delete(String conversationId) async {
    await ref.read(repositoryProvider).deleteConversation(conversationId);
    await _forgetLocal(conversationId);
    await ref.read(lastSeenStoreProvider).forget(conversationId);

    final current = state.value;
    if (current != null) {
      state = AsyncData(
        current.where((c) => c.id != conversationId).toList(growable: false),
      );
    }
  }

  /// Clears a conversation's history server-side and locally, keeping the conversation
  /// in the list. The server hides the caller's messages (a per-member watermark); here
  /// we forget the local plaintext bodies and cached envelope so nothing stale lingers.
  /// The open chat's feed is emptied by the page via MessageFeedController.clearHistory.
  Future<void> clearHistory(String conversationId) async {
    await ref.read(repositoryProvider).clearChatHistory(conversationId);
    await _forgetLocal(conversationId);
  }

  /// Forgets the local plaintext bodies and cached envelope for a conversation. The
  /// bodies cannot be recovered afterwards (MLS keys are single-use) — the point of it.
  Future<void> _forgetLocal(String conversationId) async {
    await ref.read(chatCacheProvider).forget(conversationId);
    await ref.read(chatEnvelopeCacheProvider).forget(conversationId);
  }

  /// Newest activity first.
  List<Conversation> _sorted(List<Conversation> conversations) {
    final sorted = [...conversations];
    sorted.sort((a, b) => _activity(b).compareTo(_activity(a)));
    return sorted;
  }

  String _activity(Conversation c) => c.lastMessage?.createdAt ?? c.createdAt;
}
