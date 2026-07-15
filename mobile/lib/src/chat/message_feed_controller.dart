// One conversation's messages: loading them, decrypting them, and keeping up with the live stream.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import 'dart:typed_data';

import '../crypto/chat_content.dart';
import '../crypto/mls_errors.dart';
import '../data/app_providers.dart';
import '../models/chat_models.dart';
import 'chat_providers.dart';

const _pageSize = 50;

class MessageFeedState {
  const MessageFeedState({
    this.messages = const [],
    this.contents = const {},
    this.loading = true,
    this.loadingOlder = false,
    this.joined,
    this.peerNotReady = false,
    this.cursor,
  });

  /// Oldest first. The view reverses this for display.
  final List<ChatMessage> messages;

  /// messageId -> the decrypted content. Absent means this device cannot read it, which is a real
  /// answer, not a failure — MLS gives a device no access to what was said before it joined.
  final Map<String, ChatContent?> contents;

  final bool loading;
  final bool loadingOlder;

  /// Whether this device holds the conversation's MLS group and can therefore encrypt to it.
  ///
  /// NULL MEANS "NOT KNOWN YET", and the difference matters more than it looks. It used to be a plain
  /// bool starting at false, so from the first frame until the group settled the app told the user
  /// encryption was still being set up — every single time a chat was opened, on a device that had
  /// been holding the keys for weeks. It was not setting anything up. It was waiting for the network
  /// to confirm something it already knew.
  ///
  /// A banner is only honest when we KNOW the answer is no.
  final bool? joined;

  /// Whether the other person has published no keys at all. Distinct from [joined] because the two
  /// are different problems: this one is theirs to fix, and no amount of waiting resolves it.
  final bool peerNotReady;

  /// The id to ask for the next page of older messages. Null at the start of history.
  final String? cursor;

  MessageFeedState copyWith({
    List<ChatMessage>? messages,
    Map<String, ChatContent?>? contents,
    bool? loading,
    bool? loadingOlder,
    bool? joined,
    bool? peerNotReady,
    bool clearJoined = false,
    String? cursor,
    bool clearCursor = false,
  }) {
    return MessageFeedState(
      messages: messages ?? this.messages,
      contents: contents ?? this.contents,
      loading: loading ?? this.loading,
      loadingOlder: loadingOlder ?? this.loadingOlder,
      joined: clearJoined ? null : (joined ?? this.joined),
      peerNotReady: peerNotReady ?? this.peerNotReady,
      cursor: clearCursor ? null : (cursor ?? this.cursor),
    );
  }
}

/// The one line above the composer that explains why sending may not work yet.
///
/// Exactly two reasons produce a line, and there is a third state that must produce NOTHING: we have
/// not finished asking yet. That third state used to produce one, because `joined` was a bool starting
/// at false — so every chat, every time it opened, announced that encryption was being set up for as
/// long as the network took to confirm a group this device had been holding for weeks.
///
/// Lives here, next to the state it reads, so the widget and the test cannot drift apart: there is one
/// rule, and both of them use it.
String? feedNoticeKey(MessageFeedState feed) {
  if (feed.peerNotReady) return 'chat.peerNotReady';
  // Only when we KNOW the answer is no. Null means "still asking", and the honest thing to do while
  // still asking is to say nothing at all.
  if (feed.joined == false) return 'chat.joiningOnThisDevice';
  return null;
}

class MessageFeedController extends Notifier<MessageFeedState> {
  /// Riverpod 3 has no FamilyNotifier — a family hands its argument to the notifier's constructor.
  MessageFeedController(this._conversationId);

  final String _conversationId;

  @override
  MessageFeedState build() {
    // Live messages, including the MLS control traffic that admits a new device to the group.
    ref.listen(liveEventsProvider, (_, next) {
      final event = next.value;
      if (event?.conversationId != _conversationId) return;
      final message = event?.chatMessage;
      if (message != null) _onLiveMessage(message);
    });

    Future.microtask(_load);
    return const MessageFeedState();
  }

  /// Opening a chat, in the order that makes it feel instant.
  ///
  /// The old order was: settle the group with the server (three or four round trips), THEN fetch the
  /// messages, THEN decrypt. Nothing could render until the server had confirmed a group id this
  /// device already knew, and while it waited the composer announced that encryption was being set up.
  ///
  /// The new order asks the device what it already knows, and only then asks the network:
  ///
  ///   1. the plaintext store, so anything read before is on screen at once;
  ///   2. the group ids we have written down, which is enough to decrypt — no network at all;
  ///   3. the messages;
  ///   4. and only now, in the BACKGROUND, the settle: catch up on commits, admit new devices,
  ///      prune ghosts. None of it blocks a single pixel.
  Future<void> _load() async {
    final repo = ref.read(repositoryProvider);
    final mls = ref.read(mlsServiceProvider);
    final myUserId = ref.read(myUserIdProvider);

    // Every body this device has ever read lives here and nowhere else — a message decrypts exactly
    // once — so this is not a cache warm-up, it is loading the messages.
    await ref.read(chatCacheProvider).load(_conversationId);

    // Enough to READ, from disk, without asking anyone. NOT enough to claim we are in the group — see
    // MlsService.primeGroup. That distinction is the whole safety of this fast path.
    await mls.primeGroup(_conversationId).catchError((_) {});

    // The two things we need from the network, asked for AT THE SAME TIME:
    //   * the messages;
    //   * which group is current, and whether we are in it.
    //
    // Concurrently, so confirming the group is free — it finishes while the messages are still in
    // flight, and the composer and the call button are honest by the time the first bubble lands.
    final messagesFuture = repo.listChatMessages(
      _conversationId,
      limit: _pageSize,
    );
    final confirmFuture = mls.confirmGroup(_conversationId, myUserId);

    try {
      final page = await messagesFuture;
      // The server returns newest-first; we hold oldest-first.
      final messages = page.messages.reversed
          .where((m) => !m.isControl)
          .toList(growable: false);

      state = state.copyWith(
        messages: messages,
        loading: false,
        cursor: page.nextCursor,
        clearCursor: page.nextCursor == null,
      );
      await _decrypt(messages);
    } on Object {
      state = state.copyWith(loading: false);
    }

    try {
      final groupId = await confirmFuture;
      state = state.copyWith(joined: groupId != null);

      // Confirming may have told us the conversation was RESET while we were away: the group we had
      // written down is retired, and the current one is one we have never seen. Anything that failed
      // to decrypt a moment ago against the old group may open now against the new one.
      await _decryptUnread();
    } on Object {
      // We do not know. Say nothing: `joined` stays null and the banner stays quiet. The settle below
      // gets another go.
    }

    // The heavy part — catch up on commits, admit new devices, prune ghosts — with the chat already on
    // screen and nobody waiting on it.
    await _settle();
  }

  /// Brings the group up to date with the server. Runs AFTER the chat is on screen, never before it.
  Future<void> _settle() async {
    final repo = ref.read(repositoryProvider);
    final mls = ref.read(mlsServiceProvider);
    final myUserId = ref.read(myUserIdProvider);

    try {
      final conversation = await repo.getConversation(_conversationId);
      final groupId = await mls.ensureGroup(conversation, myUserId);

      state = state.copyWith(joined: groupId != null, peerNotReady: false);

      // The settle may have caught up on commits that let us read messages we could not read a moment
      // ago — the very Welcome that admits this device, for instance. Try the ones we gave up on.
      await _decryptUnread();
    } on PeerKeysMissingException {
      state = state.copyWith(joined: false, peerNotReady: true);
    } on Object {
      // The network is down, or the settle failed. If we HOLD the group, none of that matters: we can
      // read and we can send, and telling the user encryption is being set up would be a lie. Leave
      // whatever primeGroup concluded.
    }
  }

  /// Re-tries the messages that would not decrypt, after a catch-up may have made them readable.
  ///
  /// Only the ones we have no content for. A message that DID decrypt must never be decrypted twice —
  /// the key is gone, and asking again is not merely useless, it is wrong.
  Future<void> _decryptUnread() async {
    final pending = state.messages
        .where((m) => state.contents[m.id] == null)
        .toList(growable: false);
    if (pending.isEmpty) return;

    // Drop them from the map so _decrypt does not skip them as already-attempted.
    final contents = {...state.contents};
    for (final m in pending) {
      contents.remove(m.id);
    }
    state = state.copyWith(contents: contents);

    await _decrypt(pending);
  }

  Future<void> loadOlder() async {
    final cursor = state.cursor;
    if (cursor == null || state.loadingOlder || state.loading) return;

    state = state.copyWith(loadingOlder: true);
    try {
      final page = await ref
          .read(repositoryProvider)
          .listChatMessages(_conversationId, cursor: cursor, limit: _pageSize);

      final older = page.messages.reversed
          .where((m) => !m.isControl)
          .toList(growable: false);

      state = state.copyWith(
        messages: [...older, ...state.messages],
        loadingOlder: false,
        cursor: page.nextCursor,
        clearCursor: page.nextCursor == null,
      );
      await _decrypt(older);
    } on Object {
      state = state.copyWith(loadingOlder: false);
    }
  }

  Future<void> _onLiveMessage(ChatMessage message) async {
    final mls = ref.read(mlsServiceProvider);
    final myUserId = ref.read(myUserIdProvider);

    // Control traffic never renders. It is how the group changes shape, and it has to be acted on
    // rather than shown.
    if (message.isControl) {
      if (message.contentType == ContentType.mlsDevice) {
        // Somebody's new device is asking to be let in. Any member who holds the group can admit it,
        // and they will race — the server's compare-and-set lets exactly one through.
        await mls
            .admitAnnouncedDevice(_conversationId, myUserId)
            .catchError((_) {});
        return;
      }
      // A Welcome or a Commit: the group moved, and this may be the very Welcome that lets us in.
      try {
        final conversation = await ref
            .read(repositoryProvider)
            .getConversation(_conversationId);
        final groupId = await mls.ensureGroup(conversation, myUserId);
        state = state.copyWith(joined: groupId != null);
      } on Object {
        // Nothing to do. The next open settles it.
      }
      return;
    }

    // The echo of a message we sent and already appended.
    if (state.messages.any((m) => m.id == message.id)) return;

    // Decrypt BEFORE appending, so we can skip an unreadable echo of our OWN outgoing message rather
    // than flash it as "Not available" until the send path fills it in. A sender can never decrypt its
    // own message — its plaintext lives only in the local cache the send path writes — so an echo that
    // is ours AND unreadable is exactly that message, arriving faster than send() returned. Let send()
    // render it. A message from our OTHER device decrypts fine here (a different leaf), so it is not
    // skipped.
    final content = await mls.decryptMessage(
      _conversationId,
      myUserId,
      message,
    );
    if (content == null && message.senderId == myUserId) return;

    state = state.copyWith(
      messages: [...state.messages, message],
      contents: {...state.contents, message.id: content},
    );
    await _markRead(message);
  }

  /// Decrypts what we have not already read. Every body is cached on first sight, because there is no
  /// second: MLS destroys the message key as it goes.
  ///
  /// Note what this does NOT do: snapshot state.contents, fill the snapshot, and write the whole thing
  /// back. Three call sites run this — the first page, an older page, and a live message — and they are
  /// not mutually exclusive. Two of them overlapping would each take a snapshot, each await their way
  /// through a decrypt, and the one that finished last would write back a map that never contained the
  /// other's results. The bodies survive in the local store, so nothing is lost for good, but the
  /// messages render as unreadable until the chat is reopened — which looks exactly like a decryption
  /// failure and is not one.
  ///
  /// So each result is merged into whatever state is CURRENT at the moment it lands.
  Future<void> _decrypt(List<ChatMessage> messages) async {
    if (messages.isEmpty) return;

    final mls = ref.read(mlsServiceProvider);
    final myUserId = ref.read(myUserIdProvider);

    for (final message in messages) {
      if (state.contents.containsKey(message.id)) continue;

      ChatContent? content;
      try {
        content = await mls.decryptMessage(_conversationId, myUserId, message);
      } on Object {
        // Unreadable on this device. A real answer, not a failure — and never retried, because a
        // second attempt on a consumed key is not merely useless, it is wrong.
        content = null;
      }

      state = state.copyWith(
        contents: {...state.contents, message.id: content},
      );
    }
  }

  Future<void> _markRead(ChatMessage message) => ref
      .read(lastSeenProvider.notifier)
      .markRead(_conversationId, message.createdAt);

  /// Sends a message. The content goes into the local store at send time — MLS destroys the key on
  /// encrypt, so this is the only chance we will ever have to read back what we just said.
  Future<void> send(
    Conversation conversation,
    String body, {
    String? replyTo,
    List<Uint8List> photos = const [],
  }) async {
    final message = await ref
        .read(mlsServiceProvider)
        .sendMessage(
          conversation,
          ref.read(myUserIdProvider),
          body,
          replyTo: replyTo,
          photos: photos,
        );

    // Read back what the service actually cached, so the photo references carry the blob ids the
    // message really has rather than a hopeful reconstruction of them.
    final content =
        ref.read(chatCacheProvider).content(conversation.id, message.id) ??
        ChatContent(body: body, replyTo: replyTo);

    // The live echo of this very message can arrive — and append it — BEFORE sendMessage returns here,
    // because the server round-trips faster than the local await resolves. So dedup: if it is already
    // in the list, do not add a second copy. Only _onLiveMessage checked this before, and send did not,
    // which is exactly how a sent message showed up twice.
    final alreadyThere = state.messages.any((m) => m.id == message.id);
    state = state.copyWith(
      messages: alreadyThere ? state.messages : [...state.messages, message],
      contents: {...state.contents, message.id: content},
      joined: true,
      peerNotReady: false,
    );
    await _markRead(message);
  }
}

/// autoDispose, unlike callProvider — and for the mirror-image reason.
///
/// A call outlives its screen, so its provider must too. A conversation's decrypted messages do not:
/// they are the plaintext of end-to-end encrypted messages, and holding every body of every chat the
/// user has opened in memory for the lifetime of the process is both unbounded growth and a longer
/// exposure than the feature needs. The bodies live in the local store; reopening the chat reads them
/// straight back.
///
/// It also tears down this conversation's live-event listener, which otherwise accumulated one per
/// chat ever visited.
final messageFeedProvider = NotifierProvider.autoDispose
    .family<MessageFeedController, MessageFeedState, String>(
      MessageFeedController.new,
    );
