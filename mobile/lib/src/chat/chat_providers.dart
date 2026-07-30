// The conversation layer's providers: the crypto service, the list, and one open chat.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

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
    // Tell the server which MLS device this push address belongs to, as soon as there IS one.
    // Without this the link waited for the next app launch, and message previews — which the
    // server withholds from an address it cannot trace to an identity — silently did not work
    // until then. See MlsService.onIdentityMinted.
    //
    // ref.read, not watch: this runs long after the provider is built, and re-reading the
    // controller here must not rebuild the MLS service — rebuilding it would drop the session.
    onIdentityMinted: () => unawaited(
      ref.read(deviceControllerProvider.notifier).linkMlsIdentity(),
    ),
  ),
);

/// The signed-in user's id.
///
/// Taken from the auth state, which CHANGES when somebody signs in or out. It used to be decoded
/// from `tokenStoreProvider.current`, and that is a one-shot read wearing a watch: the store is a
/// plain Provider whose instance never changes, so watching it never fires again and this provider
/// kept whatever it computed the first time anything asked. Read once before sign-in — which is the
/// normal order of events on a cold start — it stayed at '' for the rest of the run.
///
/// Nothing announced that. An empty id simply means "no member matches me", so
/// [Conversation.otherMember] returned the FIRST member instead, which in a chat you started is
/// you: open a new chat with anyone and the header showed your own name and your own face. It
/// corrected itself on the next launch, which is the worst kind of bug — one that disappears when
/// you go looking for it.
final myUserIdProvider = Provider<String>((ref) {
  return ref.watch(authControllerProvider.select((s) => s.userId)) ?? '';
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

  /// The furthest seq already reported to the server, so a receipt goes once per advance rather
  /// than every time the feed touches the bottom.
  final _reportedSeq = <String, int>{};

  /// [at] is the timestamp of the newest message displayed — kept locally to drive the unread dot,
  /// which never leaves the device. [seq] is that message's sequence, reported to the server so the
  /// sender's ticks fill in.
  Future<void> markRead(String conversationId, String at, int seq) async {
    await ref.read(lastSeenStoreProvider).markRead(conversationId, at);
    state = AsyncData({...?state.value, conversationId: at});

    // Tell the sender their newest read message has been read. Only ever forwards, and only when it
    // actually moves. Messages predating sequencing carry seq 0, which has no watermark to move and
    // the server rejects.
    if (seq == 0) return;
    if (seq <= (_reportedSeq[conversationId] ?? 0)) return;
    _reportedSeq[conversationId] = seq;
    unawaited(
      ref
          .read(repositoryProvider)
          .reportReceipt(conversationId, readSeq: seq)
          .catchError((Object _) {
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
  final cache = ref.watch(chatCacheProvider);
  final last = conversation.lastMessage;
  // Pinned to the message id: a preview left over from an OLDER message says nothing about who
  // wrote this one, and reading it as if it did would be the same misattribution in miniature.
  final authenticated =
      last != null && cache.previewMessageId(conversation.id) == last.id
      ? cache.previewSender(conversation.id)
      : '';
  return isConversationUnread(
    last: last,
    myUserId: ref.watch(myUserIdProvider),
    seenAt: ref.watch(lastSeenProvider).value?[conversation.id],
    baseline: ref.watch(readBaselineProvider).value,
    authenticatedSender: authenticated,
  );
});

/// When this device first looked at its chats; history older than this is treated as read.
///
/// Read state lives on the device and does not sync, so a fresh install has no record of anything.
/// Without a baseline every conversation the account has ever had lights up unread the moment you
/// sign in on a new phone — which is not information, it is noise, and it hides the one chat that
/// genuinely does have something new.
///
/// A baseline cannot be right, only least wrong. This device honestly does not know what was read
/// elsewhere; assuming everything up to the moment it arrived has been dealt with is the assumption
/// that costs the user nothing when it is wrong, because anything arriving afterwards still counts.
/// The real answer is read state on the server, which is a larger change.
final readBaselineProvider = FutureProvider<String>((ref) async {
  final store = ref.watch(settingsStoreProvider);
  final existing = await store.loadReadBaseline();
  if (existing != null && existing.isNotEmpty) return existing;
  final now = DateTime.now().toUtc().toIso8601String();
  await store.saveReadBaseline(now);
  return now;
});

/// The unread rule, as a plain function so it can be tested without a provider container.
///
/// [seenAt] is the timestamp of the newest message this device has DISPLAYED. Comparison is lexical
/// on the server's ISO-8601 UTC timestamps, which sort correctly as strings precisely because the
/// server writes them in one fixed shape.
bool isConversationUnread({
  required LastChatMessage? last,
  required String myUserId,
  required String? seenAt,
  String? baseline,
  String authenticatedSender = '',
}) {
  if (last == null) return false;
  // Protocol traffic is not a message; nobody wrote it and there is nothing to read.
  if (ContentType.control.contains(last.contentType)) return false;
  // Neither is a line the conversation says about itself — somebody joining or leaving is worth
  // seeing when you next look, and is not a message waiting for you.
  if (ContentType.system.contains(last.contentType)) return false;
  // Answered from the AUTHENTICATED sender where this device has one — the chat list cannot decrypt
  // anything (the message key is spent on first read), so its only source is what the open
  // conversation recorded in the body cache. The envelope's senderId is the fallback and only the
  // fallback: it is written by the server, which would otherwise get to decide whether a chat looks
  // unread.
  final sender = authenticatedSender.isNotEmpty
      ? authenticatedSender
      : last.senderId;
  if (sender == myUserId) return false;

  // Nothing recorded for this conversation. On a device that has been here a while that means
  // genuinely unseen; on a fresh install it means only that this phone was not present for it — so
  // fall back to the baseline rather than declaring the entire account unread.
  if (seenAt == null) {
    if (baseline == null) return true;
    return last.createdAt.compareTo(baseline) > 0;
  }
  return last.createdAt.compareTo(seenAt) > 0;
}

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
