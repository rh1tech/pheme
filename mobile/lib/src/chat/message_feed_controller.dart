// One conversation's messages: loading them, decrypting them, and keeping up with the live stream.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../crypto/mls_errors.dart';
import '../data/app_providers.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import 'chat_providers.dart';

const _pageSize = 50;

class MessageFeedState {
  const MessageFeedState({
    this.messages = const [],
    this.bodies = const {},
    this.loading = true,
    this.loadingOlder = false,
    this.joined = false,
    this.peerNotReady = false,
    this.cursor,
  });

  /// Oldest first. The view reverses this for display.
  final List<ChatMessage> messages;

  /// messageId -> plaintext. Absent means this device cannot read it, which is a real answer.
  final Map<String, String?> bodies;

  final bool loading;
  final bool loadingOlder;

  /// Whether this device holds the conversation's MLS group and can therefore encrypt to it.
  final bool joined;

  /// Whether the other person has published no keys at all. Distinct from [joined] because the two
  /// are different problems: this one is theirs to fix, and no amount of waiting resolves it.
  final bool peerNotReady;

  /// The id to ask for the next page of older messages. Null at the start of history.
  final String? cursor;

  MessageFeedState copyWith({
    List<ChatMessage>? messages,
    Map<String, String?>? bodies,
    bool? loading,
    bool? loadingOlder,
    bool? joined,
    bool? peerNotReady,
    String? cursor,
    bool clearCursor = false,
  }) {
    return MessageFeedState(
      messages: messages ?? this.messages,
      bodies: bodies ?? this.bodies,
      loading: loading ?? this.loading,
      loadingOlder: loadingOlder ?? this.loadingOlder,
      joined: joined ?? this.joined,
      peerNotReady: peerNotReady ?? this.peerNotReady,
      cursor: clearCursor ? null : (cursor ?? this.cursor),
    );
  }
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

  Future<void> _load() async {
    final repo = ref.read(repositoryProvider);
    final mls = ref.read(mlsServiceProvider);
    final myUserId = ref.read(myUserIdProvider);

    // Warm the plaintext store before anything renders. Every body this device has ever read lives
    // there and nowhere else — a message decrypts exactly once.
    await ref.read(chatCacheProvider).load(_conversationId);

    // Getting into the group is what makes reading possible, so it comes first. A null group id is a
    // normal state — this device has announced itself and is waiting to be admitted — not a failure.
    var joined = false;
    var peerNotReady = false;
    try {
      final conversation = await repo.getConversation(_conversationId);
      joined = await mls.ensureGroup(conversation, myUserId) != null;
    } on PeerKeysMissingException {
      peerNotReady = true;
    } on Object {
      // Reading history may still work — this device may hold the group from an earlier session.
    }

    try {
      final page = await repo.listChatMessages(
        _conversationId,
        limit: _pageSize,
      );
      // The server returns newest-first; we hold oldest-first.
      final messages = page.messages.reversed
          .where((m) => !m.isControl)
          .toList(growable: false);

      state = state.copyWith(
        messages: messages,
        loading: false,
        joined: joined,
        peerNotReady: peerNotReady,
        cursor: page.nextCursor,
        clearCursor: page.nextCursor == null,
      );
      await _decrypt(messages);
    } on Object {
      state = state.copyWith(
        loading: false,
        joined: joined,
        peerNotReady: peerNotReady,
      );
    }
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

    state = state.copyWith(messages: [...state.messages, message]);
    await _decrypt([message]);
    await _markRead(message);
  }

  /// Decrypts what we have not already read. Every body is cached on first sight, because there is no
  /// second: MLS destroys the message key as it goes.
  ///
  /// Note what this does NOT do: snapshot state.bodies, fill the snapshot, and write the whole thing
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
      if (state.bodies.containsKey(message.id)) continue;

      String? body;
      try {
        body = await mls.decryptMessage(_conversationId, myUserId, message);
      } on Object {
        // Unreadable on this device. A real answer, not a failure — and never retried, because a
        // second attempt on a consumed key is not merely useless, it is wrong.
        body = null;
      }

      state = state.copyWith(bodies: {...state.bodies, message.id: body});
    }
  }

  Future<void> _markRead(ChatMessage message) => ref
      .read(lastSeenProvider.notifier)
      .markRead(_conversationId, message.createdAt);

  /// Sends a message. The body goes into the local store at send time — MLS destroys the key on
  /// encrypt, so this is the only chance we will ever have to read back what we just said.
  Future<void> send(Conversation conversation, String body) async {
    final message = await ref
        .read(mlsServiceProvider)
        .sendMessage(conversation, ref.read(myUserIdProvider), body);

    state = state.copyWith(
      messages: [...state.messages, message],
      bodies: {...state.bodies, message.id: body},
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
