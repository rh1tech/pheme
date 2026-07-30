// Device-to-device history sync, driven off the app-wide live stream.
//
// A device that joins a conversation it holds no transcript for posts a history REQUEST (see
// MlsService.requestHistory). Its other same-account devices hear it here; ONE of them answers with
// a sealed blob, and the requester opens it. All of it is sealed under a group-derived key.
//
// ELECTION. Only same-account devices are eligible: a different participant can sign with a valid
// leaf but cannot vouch for the requester's history. Candidates wait a rank-based delay and stand
// down when an offer has already appeared.

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../crypto/history_handoff.dart';
import '../crypto/mls_service.dart';
import '../crypto/attribution.dart';
import '../data/app_providers.dart';
import '../models/chat_models.dart';
import '../models/models.dart';
import 'chat_providers.dart';
import 'message_feed_controller.dart';

/// How long each rank-step of the election waits. Small — the point is a prompt handoff.
const _electionStepMs = 400;

class HistorySyncController extends Notifier<void> {
  /// Conversations we have already re-asked about after losing an offer, so a device that cannot
  /// store anything asks once more and then stops rather than churning offers forever.
  final _reRequested = <String>{};

  /// Asks for the history again after an offer was consumed and then lost.
  ///
  /// The offer's blob is deleted by the server as it is read, so a failed import does not leave
  /// anything to retry — the only way back is for a co-device to seal a fresh one. requestHistory
  /// refuses to ask twice in a session on its own, so this clears that guard once and once only.
  void _reRequest(String conversationId, String userId) {
    if (!_reRequested.add(conversationId)) return;
    final mls = ref.read(mlsServiceProvider);
    unawaited(
      mls
          .forgetHistoryRequest(conversationId)
          .then((_) => mls.requestHistory(conversationId, userId)),
    );
  }

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
      _reRequested.clear();
    });
  }

  void _markOffered(String id) {
    // Cap the set to prevent unbounded growth over long sessions. Dart Sets
    // preserve insertion order, so removing first evicts the oldest entry.
    if (_offered.length >= 200) {
      _offered.remove(_offered.first);
    }
    _offered.add(id);
  }

  void _onEvent(LiveEvent event) {
    final userId = ref.read(myUserIdProvider);
    final conversationId = event.conversationId;
    final message = event.chatMessage;
    if (userId.isEmpty || conversationId == null || message == null) return;

    if (message.contentType == ContentType.mlsHistoryOffer) {
      // Record that this conversation has an offer, so an in-flight election stands down. Then try
      // to receive it — receiveHistoryOffer ignores offers not addressed to this device.
      _markOffered('$conversationId:${message.id}');
      final mls = ref.read(mlsServiceProvider);
      unawaited(
        mls
            .receiveHistoryOffer(
              conversationId,
              userId,
              message.ciphertext,
              // The server authenticates the POSTER of a control message. That is a second,
              // independent witness alongside the MLS signature: an insider forging an offer in
              // another member's name has to post it from that member's account as well.
              posterId: message.senderId,
            )
            .then((result) {
              switch (result) {
                case HistoryOfferResult.accepted:
                  // A fresh cache write does not repaint an open conversation on its own; nudge it.
                  ref.invalidate(messageFeedProvider(conversationId));
                case HistoryOfferResult.lost:
                  // The offer was fetched and then could not be kept, and fetching CONSUMED it —
                  // the server deletes the blob as it is read. Nobody will offer again unless we
                  // ask again, so the history would simply never arrive. Asking is bounded by
                  // requestHistory's own once-per-conversation-per-session rule, so a device whose
                  // cache is permanently broken retries once and then stops.
                  _reRequest(conversationId, userId);
                case HistoryOfferResult.ignored:
                case HistoryOfferResult.refused:
                  // Nothing consumed, or refused on its merits. Asking again would fetch the same
                  // answer.
                  break;
              }
            })
            .catchError((_) {}),
      );
      return;
    }

    if (message.contentType != ContentType.mlsHistoryRequest) return;
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
    // v1 requests — unsigned — parse to null and are never answered. Answering one means sealing a
    // whole conversation to a key derived for an identity that may never have asked for it.
    final request = parseRequestBody(message.ciphertext);
    if (request == null) return;

    final mls = ref.read(mlsServiceProvider);
    final identity = await mls.myIdentity(userId).catchError((_) => '');
    final members = await mls
        .groupMemberIdentities(conversationId, userId)
        .catchError((_) => <String>[]);
    final eligible = members
        .where((member) => sameAccountIdentities(member, request.id))
        .toList();
    // Only another device of the requester account may answer. Every group participant owns a
    // valid leaf key, so accepting arbitrary members would let one sign invented history as itself.
    if (identity.isEmpty ||
        identity == request.id ||
        !sameAccountIdentities(identity, request.id) ||
        eligible.isEmpty) {
      return;
    }

    // Rank among this account's eligible devices sets our election delay.
    final sorted = [...eligible]..sort();
    final rank = sorted.indexOf(identity);
    final delay = Duration(
      milliseconds: _electionStepMs * (rank < 0 ? eligible.length : rank),
    );
    final timer = Timer(delay, () {
      _answering.remove(key);
      // Someone already answered this conversation's request — stand down.
      if (_offered.any((seen) => seen.startsWith('$conversationId:'))) return;
      unawaited(
        mls
            .offerHistory(
              conversationId,
              userId,
              request,
              posterId: message.senderId,
            )
            .catchError((_) {}),
      );
    });
    _answering[key] = timer;
  }
}

/// Mounted app-wide (watched by the recovery gate that wraps the home surface), so a request is
/// answered by whoever has the app open — not only someone looking at the conversation it concerns.
final historySyncControllerProvider =
    NotifierProvider<HistorySyncController, void>(HistorySyncController.new);
