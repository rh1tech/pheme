// Device-to-device history sync, driven off the app-wide live stream.
//
// A device that joins a conversation it holds no transcript for posts a history REQUEST (see
// MlsService.requestHistory). Every co-member that holds the group hears it here; ONE of them
// answers with a sealed blob, and the requester opens it. All of it is sealed under a key derived
// from the group — the server relays pointers, never content.
//
// ELECTION. Every co-member could answer; we want ~one. Each candidate waits a short delay keyed by
// its RANK among the group's members (lowest identity answers soonest), and stands down if an offer
// for this conversation has meanwhile appeared. First writer wins; the rest suppress. This tolerates
// the lossy stream without needing to know who else is online.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/app_providers.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import 'chat_providers.dart';
import 'message_feed_controller.dart';

/// How long each rank-step of the election waits. Small — the point is a prompt handoff.
const _electionStepMs = 400;

class HistorySyncController extends Notifier<void> {
  /// Requests we have scheduled a response to, so a repeated event does not double-answer.
  final _answering = <String, Timer>{};

  /// Offers seen per conversation, so an elected candidate can tell someone already answered.
  final _offered = <String>{};

  @override
  void build() {
    ref.listen(liveEventsProvider, (_, next) {
      final event = next.value;
      if (event != null) _onEvent(event);
    });
    ref.onDispose(() {
      for (final timer in _answering.values) {
        timer.cancel();
      }
      _answering.clear();
      _offered.clear();
    });
  }

  void _onEvent(LiveEvent event) {
    final userId = ref.read(myUserIdProvider);
    final conversationId = event.conversationId;
    final message = event.chatMessage;
    if (userId.isEmpty || conversationId == null || message == null) return;

    if (message.contentType == ContentType.mlsHistoryOffer) {
      // Record that this conversation has an offer, so an in-flight election stands down. Then try
      // to receive it — receiveHistoryOffer ignores offers not addressed to this device.
      _offered.add('$conversationId:${message.id}');
      final mls = ref.read(mlsServiceProvider);
      unawaited(
        mls
            .receiveHistoryOffer(conversationId, userId, message.ciphertext)
            .then((imported) {
              // A fresh cache write does not repaint an open conversation on its own; nudge it.
              if (imported) {
                ref.invalidate(messageFeedProvider(conversationId));
              }
            })
            .catchError((_) {}),
      );
      return;
    }

    if (message.contentType != ContentType.mlsHistoryRequest) return;
    // Our own user's request (this device, or another of ours) — a co-member of another user answers.
    if (message.senderId == userId) return;
    final key = '$conversationId:${message.id}';
    if (_answering.containsKey(key)) return;
    unawaited(_electAndAnswer(conversationId, userId, key, message));
  }

  Future<void> _electAndAnswer(
    String conversationId,
    String userId,
    String key,
    ChatMessage message,
  ) async {
    final mls = ref.read(mlsServiceProvider);
    final identity = await mls.myIdentity(userId).catchError((_) => '');
    final members = await mls
        .groupMemberIdentities(conversationId, userId)
        .catchError((_) => <String>[]);
    // We do not hold the group, so we cannot help.
    if (members.isEmpty || identity.isEmpty) return;

    // Rank among the current members sets our election delay. Lower identity → sooner.
    final sorted = [...members]..sort();
    final rank = sorted.indexOf(identity);
    final delay = Duration(
      milliseconds: _electionStepMs * (rank < 0 ? members.length : rank),
    );
    final requester = _requesterOf(message.ciphertext);
    final timer = Timer(delay, () {
      _answering.remove(key);
      // Someone already answered this conversation's request — stand down.
      if (_offered.any((seen) => seen.startsWith('$conversationId:'))) return;
      unawaited(
        mls.offerHistory(conversationId, userId, requester).catchError((_) {}),
      );
    });
    _answering[key] = timer;
  }

  /// Pulls the requester's identity out of a history-request control body. Empty on a malformed one.
  String _requesterOf(Uint8List ciphertext) {
    try {
      final parsed = jsonDecode(utf8.decode(ciphertext));
      if (parsed is Map && parsed['id'] is String) {
        return parsed['id'] as String;
      }
      return '';
    } on Object {
      return '';
    }
  }
}

/// Mounted app-wide (watched by the recovery gate that wraps the home surface), so a request is
/// answered by whoever has the app open — not only someone looking at the conversation it concerns.
final historySyncControllerProvider =
    NotifierProvider<HistorySyncController, void>(HistorySyncController.new);
