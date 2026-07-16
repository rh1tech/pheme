// The conversation layer's providers: the crypto service, the list, and one open chat.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/jwt.dart';
import '../core/providers.dart';
import '../crypto/chat_cache.dart';
import '../crypto/chat_envelope_cache.dart';
import '../crypto/mls_service.dart';
import '../models/chat_models.dart';
import 'conversation_list_controller.dart';
import 'last_seen_store.dart';

/// The decrypted-body store. One per session, because it is the only copy of every message this
/// device has ever read.
final chatCacheProvider = Provider<ChatCache>(
  (ref) => ChatCache(ref.watch(secureStorageProvider)),
);

/// The message-envelope store: the ordered list of message metadata per conversation, so a chat
/// paints its last-seen transcript from disk the instant it opens, before the network answers.
final chatEnvelopeCacheProvider = Provider<ChatEnvelopeCache>(
  (ref) => ChatEnvelopeCache(ref.watch(secureStorageProvider)),
);

/// The conversation the user is currently looking at, or null when none is open. Set by
/// ConversationChatPage on open and cleared on leave; read by the push service to suppress a
/// notification for a chat that is already on screen.
class ActiveConversationController extends Notifier<String?> {
  @override
  String? build() => null;

  void set(String? conversationId) => state = conversationId;
}

final activeConversationIdProvider =
    NotifierProvider<ActiveConversationController, String?>(
      ActiveConversationController.new,
    );

/// The MLS orchestration. One per session: the group state, the in-flight settles and the call
/// freeze are all shared by everything that touches a conversation, so there can only be one.
final mlsServiceProvider = Provider<MlsService>(
  (ref) => MlsService(
    repository: ref.watch(repositoryProvider),
    storage: ref.watch(secureStorageProvider),
    cache: ref.watch(chatCacheProvider),
  ),
);

/// The signed-in user's id, decoded from the access token.
final myUserIdProvider = Provider<String>((ref) {
  final tokens = ref.watch(tokenStoreProvider).current;
  if (tokens == null) return '';
  return decodeUserId(tokens.accessToken) ?? '';
});

/// Per-conversation read state. Local to this device on purpose — an unread dot that does not sync
/// across devices is a smaller lie than a count that cannot be computed, because the server cannot
/// read the messages it would have to count.
final lastSeenStoreProvider = Provider<LastSeenStore>(
  (ref) => LastSeenStore(ref.watch(secureStorageProvider)),
);

/// conversationId -> the ISO timestamp of the newest message the user has seen there.
///
/// Held in a notifier rather than read per row, so a list of fifty conversations does not do fifty
/// async reads to draw fifty dots.
class LastSeenController extends AsyncNotifier<Map<String, String>> {
  @override
  Future<Map<String, String>> build() => ref.watch(lastSeenStoreProvider).all();

  /// The furthest point already reported to the server, so a receipt goes once per advance rather
  /// than every time the feed touches the bottom.
  final _reported = <String, String>{};

  Future<void> markRead(String conversationId, String at) async {
    await ref.read(lastSeenStoreProvider).markRead(conversationId, at);
    state = AsyncData({...?state.value, conversationId: at});

    // This device has displayed up to here, which is what two ticks mean. Only ever forwards, and
    // only when it actually moves.
    if (at.compareTo(_reported[conversationId] ?? '') <= 0) return;
    _reported[conversationId] = at;
    unawaited(
      ref.read(repositoryProvider).reportReceipt(conversationId, read: at).catchError((
        Object _,
      ) {
        // A receipt is a courtesy: a lost one costs a tick until the next advance, never a
        // message. Not rolled back — retrying every failure would hammer a struggling server.
      }),
    );
  }
}

final lastSeenProvider =
    AsyncNotifierProvider<LastSeenController, Map<String, String>>(
      LastSeenController.new,
    );

/// Whether a conversation has something in it the user has not read.
///
/// A message from us is never unread, and control traffic is not a message at all — the user has
/// nothing to read, so it must not light up a dot.
final unreadProvider = Provider.family<bool, Conversation>((ref, conversation) {
  final last = conversation.lastMessage;
  if (last == null) return false;
  if (ContentType.control.contains(last.contentType)) return false;
  if (last.senderId == ref.watch(myUserIdProvider)) return false;

  final seen = ref.watch(lastSeenProvider).value?[conversation.id];
  if (seen == null) return true;
  return last.createdAt.compareTo(seen) > 0;
});

/// The conversation list, patched live from the event stream.
final conversationListProvider =
    AsyncNotifierProvider<ConversationListController, List<Conversation>>(
      ConversationListController.new,
    );

/// One conversation, by id. Loaded from the server so it works on a cold deep-link from a push,
/// where the list has not been fetched yet.
final conversationProvider = FutureProvider.family<Conversation, String>(
  (ref, id) => ref.watch(repositoryProvider).getConversation(id),
);

/// Whether the server can do calls at all.
///
/// It answers 503 when no TURN is configured, and that is not a transient failure — it is how the
/// client learns not to offer a call button in the first place. Asked once per session.
final callingAvailableProvider = FutureProvider<bool>((ref) async {
  try {
    await ref.watch(repositoryProvider).iceServers();
    return true;
  } on Object {
    return false;
  }
});
