// The group lifecycle: getting this device into a conversation's MLS group, and keeping every other
// member's devices in it too. A port of web/src/lib/mls.ts, which is the reference implementation.
//
// The server is the untrusted delivery service throughout — it only ever sees the opaque bytes these
// functions hand it. But it is the one party every member agrees on, and two questions can only be
// settled there: which group a conversation IS, and whose Commit came first. Both are settled by one
// compare-and-set (mlsCommit).
//
// ---------------------------------------------------------------------------------------------
// THE RULE EVERYTHING HERE FOLLOWS: AN MLS LEAF IS A DEVICE, NOT A PERSON.
//
// Two devices of the same user are two independent clients with different private keys. Each must be
// its own leaf, or it cannot decrypt a single message — which is what a chat full of blanks actually
// is. So:
//
//   * a group is built from one key package per DEVICE of each member, never one per user;
//   * a device missing from the group is ADDED to it, and the group is never torn down and rebuilt
//     around it — rebuilding destroys the key material for every message anyone ever sent;
//   * removing someone removes EVERY leaf they hold, or they carry on reading on their phone.
// ---------------------------------------------------------------------------------------------

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../core/api_exception.dart';
import '../data/pheme_repository.dart';
import '../models/chat_models.dart';
import '../rust/api/mls.dart' as rust;
import 'chat_cache.dart';
import 'chat_content.dart';
import 'image_size.dart';
import 'photo_crypto.dart';
import 'mls_device.dart';
import 'mls_errors.dart';
import 'mls_session.dart';
import 'mls_store.dart';

/// Keep at least this many unclaimed key packages published, so a peer can always start a chat;
/// replenish up to the target when it runs low.
const _minKeyPackages = 5;
const _targetKeyPackages = 20;

/// How many times a Commit is re-proposed after the server refuses it. Each round trip means another
/// member committed first. A handful of concurrent membership changes is realistic; an unbounded
/// fight is not.
const _commitAttempts = 4;

/// How long a device announces itself and waits to be admitted before concluding nobody is coming.
///
/// Long enough that a member who was going to admit it would have — admission happens from anywhere
/// in the app, so it needs somebody to have Pheme open at all, not to be looking at this chat. Short
/// enough that a person staring at "setting up encryption" is not left there.
const _stuckAfter = Duration(seconds: 90);

/// How many times an external join is retried when it keeps losing the compare-and-set. Each loss
/// means another member committed between fetching the GroupInfo and offering the external commit — a
/// live conversation, not a fight, so a few retries is plenty before falling back.
const _externalJoinAttempts = 3;

/// The exporter label for call keys. Changing it changes every derived key, so it is versioned.
const callKeyLabel = 'pheme-call-v1';

/// The MLS orchestration for the signed-in user.
///
/// One per session. Held by a provider so the group state, the in-flight settles and the call freeze
/// are shared by everything that touches a conversation.
class MlsService {
  /// [namespace] separates one device's storage from another's in the same process. Empty in the app;
  /// the integration tests use it to run two devices at once.
  MlsService({
    required PhemeRepository repository,
    required FlutterSecureStorage storage,
    required this._cache,
    String namespace = '',
  }) : _repo = repository,
       _storage = storage,
       _ns = namespace,
       _store = MlsStore(storage, namespace: namespace);

  final String _ns;

  final PhemeRepository _repo;
  final FlutterSecureStorage _storage;
  final ChatCache _cache;
  final MlsStore _store;

  Future<MlsSession>? _session;
  String _sessionUserId = '';

  /// Memoized answer to "must this device restore before it can have an identity?". Cleared whenever
  /// the answer could change — a wipe, a restore, or the user choosing to start fresh.
  bool? _restoreNeeded;

  /// ensureGroup in flight, per conversation. Several callers ask at once — opening the chat, sending
  /// into it, a device announcing itself — and they must not all claim key packages and all try to
  /// establish the group.
  final _settling = <String, Future<String?>>{};

  /// Every group of a conversation this device could read a message from: the current one first, then
  /// any that have been retired. A message from before a reset is still perfectly readable.
  final _readableGroups = <String, List<String>>{};

  /// How long this device has been waiting to be let in, per conversation. In memory on purpose: a
  /// reset is a last resort, and it should take a sustained period of a live device asking and
  /// getting nowhere — not a stale timestamp from last week.
  final _waitingSince = <String, DateTime>{};

  /// Calls in flight. A counter and not a flag, because a second call can start before the first has
  /// finished tearing down, and the first one's cleanup must not unfreeze the second.
  int _callsInProgress = 0;

  // --- session ---------------------------------------------------------------------------------

  /// This device's session, loading it on first use.
  ///
  /// Cached per user: if a different account signs in without a restart, the cached one is discarded
  /// rather than reused. Reusing it would encrypt the new user's messages under the previous user's
  /// MLS identity.
  Future<MlsSession> session(String userId) {
    final cached = _session;
    if (cached != null && _sessionUserId == userId) return cached;

    _sessionUserId = userId;
    // A failed load must not be cached, or the app would be permanently unable to build a session —
    // including after the user restores from their backup and tries again.
    final loading = _load(userId).onError<Object>((e, s) {
      _session = null;
      _sessionUserId = '';
      Error.throwWithStackTrace(e, s);
    });
    return _session = loading;
  }

  Future<MlsSession> _load(String userId) async {
    final session = await MlsSession.load(
      store: _store,
      userId: userId,
      storedDeviceId: await loadMlsDeviceId(_storage, namespace: _ns),
      rememberDeviceId: (id) => saveMlsDeviceId(_storage, id, namespace: _ns),
      mustRestore: await _needsRestore(userId),
    );

    // Everything below this line is HOUSEKEEPING, and none of it blocks the session.
    //
    // Purging a dead identity's key packages, and topping up the stock of live ones, are both about
    // being REACHABLE — about someone else being able to start a chat with this device. Neither has
    // the slightest bearing on whether this device can read the conversations it is already in.
    //
    // They used to be awaited, so the first chat opened after launch waited on two network calls
    // before it could show a single message, and while it waited the composer said encryption was
    // still being set up. It was not. It was publishing key packages.
    unawaited(_houseKeeping(session));
    return session;
  }

  /// Purge the retired identity's key packages, and top up the stock. Best effort, in the background.
  Future<void> _houseKeeping(MlsSession session) async {
    // The identity this one replaces may still have public key packages on the server. Their private
    // halves are gone, so anyone claiming one would build a group this device could never join. Purge
    // them before publishing new ones — and purging them is also what tells everyone else that leaf
    // is a ghost, so the group can prune it.
    final retired = session.retiredDeviceId;
    if (retired.isNotEmpty) {
      await _repo.deleteKeyPackages(retired).catchError((_) {});
    }

    try {
      await _replenishKeyPackages(session);
    } on Object {
      // A peer starting a chat will find no package and simply have to retry. Nothing this device is
      // already in is affected.
    }
  }

  /// Publishes fresh key packages when the server's stock runs low, and makes sure this device has
  /// published its one reusable last-resort package.
  ///
  /// Single-use packages can be claimed by anyone, so a stranger can drain them. The last-resort
  /// package is what guarantees the user stays reachable: an RFC 9420 extension makes the client KEEP
  /// its private key instead of deleting it after first use, so it can be handed out again and again.
  Future<void> _replenishKeyPackages(MlsSession session) async {
    final MLSKeyPackageCount status;
    try {
      status = await _repo.keyPackageCount(session.deviceId);
    } on Object {
      return; // best effort; a peer starting a chat will just have to retry
    }

    final needStock = status.count < _minKeyPackages;
    final needLastResort = !status.hasLastResort;
    if (!needStock && !needLastResort) return;

    final minted = await session.mintKeyPackages(
      count: needStock ? _targetKeyPackages - status.count : 0,
      lastResort: needLastResort,
    );
    await _repo.publishKeyPackages(
      session.deviceId,
      minted.packages,
      lastResortKeyPackage: minted.lastResort,
    );
  }

  // --- group lifecycle -------------------------------------------------------------------------

  /// Makes sure this device is in the conversation's group, and that every device of every member is
  /// too. Returns the group id once this device can encrypt to it, or null while it cannot yet.
  ///
  /// NULL IS A NORMAL STATE, NOT A FAILURE. A device that has just been added has to wait for a
  /// member to notice and admit it. It is not stuck: it has said so, and it will be let in.
  Future<String?> ensureGroup(Conversation conversation, String myUserId) {
    final inFlight = _settling[conversation.id];
    if (inFlight != null) return inFlight;

    final run = _settleGroup(conversation, myUserId)
        .then((groupId) {
          // The group we ended up in goes at the FRONT of what we can read. _settleGroup records what
          // the server told it — but the server may have said there was no group at all, and then this
          // device went and established one. Without this, the device that created the group could not
          // decrypt a single message in it, its own history included.
          if (groupId != null) _rememberReadable(conversation.id, groupId);
          return groupId;
        })
        .whenComplete(() {
          _settling.remove(conversation.id);
        });

    _settling[conversation.id] = run;
    return run;
  }

  /// Makes a conversation READABLE from what this device already knows, without asking the server.
  ///
  /// This is the whole answer to "why does it say encryption is being set up every time I open a
  /// chat". It was not being set up. The device was holding the ratchet the entire time; the only
  /// thing it lacked was the group's ID — and waiting three round trips to be told a string it already
  /// knew meant nothing could decrypt, nothing could render, and the only thing the UI could honestly
  /// say was "still working on it".
  ///
  /// -----------------------------------------------------------------------------------------------
  /// WHAT THIS DOES NOT DO, AND MUST NOT: say that this device is IN the group.
  ///
  /// A remembered id is enough to READ. It is not proof of membership, and it is not the group to
  /// encrypt to. If another device reset the conversation, the current group is one we have never
  /// heard of — and a client that trusted this cache would cheerfully encrypt its next message to the
  /// RETIRED group. Everyone else is on the new one. Nobody could read it. Nothing would report an
  /// error, because nothing went wrong: the message was sealed perfectly, to a group nobody is in.
  ///
  /// Reading is safe because it cannot lie in that direction: a message from the old group still opens
  /// with the old group, and one from the new group simply does not open — which is a miss, not a
  /// forgery, and [confirmGroup] repairs it a moment later.
  ///
  /// Only the server can say which group is current. See [confirmGroup].
  /// -----------------------------------------------------------------------------------------------
  Future<void> primeGroup(String conversationId) async {
    final known = await _store.groupIds(conversationId);
    if (known.isEmpty) return;

    // Every group the conversation has ever had. A message from before a reset was encrypted to one
    // that is no longer current, and it is still perfectly readable by a device that still holds it.
    _readableGroups[conversationId] = known;
  }

  /// Asks the server which group is current, and whether this device is in it. ONE round trip.
  ///
  /// The authoritative answer, and the only one that may enable sending or calling. It is deliberately
  /// separate from [ensureGroup], which also catches up on commits, admits new devices and prunes
  /// ghosts — worth doing, and worth doing in the background, but not worth making the user wait for.
  ///
  /// Returns the current group id when this device holds it; null when it does not — which is a real
  /// answer, and the one case the "setting up encryption" banner exists for.
  ///
  /// It also REPAIRS THE CACHE. If another device reset the conversation, the id we had written down is
  /// retired, and this is where we find out: the old group stays readable, the new one becomes the one
  /// we must be admitted to, and [ensureGroup] does the admitting.
  Future<String?> confirmGroup(String conversationId, String myUserId) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);

    if (state.allGroupIds.isNotEmpty) {
      _readableGroups[conversationId] = state.allGroupIds;
      await _store
          .rememberGroupIds(conversationId, state.allGroupIds)
          .catchError((_) {});
    }

    if (!state.isEstablished) {
      return null;
    }
    final holds = await session.hasGroup(state.groupId);
    return holds ? state.groupId : null;
  }

  Future<String?> _settleGroup(
    Conversation conversation,
    String myUserId,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversation.id);

    _readableGroups[conversation.id] = state.allGroupIds;
    // Write it down, so the NEXT open of this chat needs none of this.
    if (state.isEstablished) {
      await _store.rememberGroupIds(conversation.id, state.allGroupIds);
    }

    // Nobody has established a group yet, so somebody must.
    //
    // ANY member does it, not just the creator. Reserving it for the creator looks tidy — it avoids a
    // loser burning the key packages it claimed — but it is a deadlock: if the creator never opens the
    // chat, everyone else sits at "setting up encryption" forever with no way to make progress. The
    // server's compare-and-set already makes the race safe; one of them simply loses. A wasted key
    // package is a rounding error next to a conversation that never works.
    if (!state.isEstablished) {
      return _establishGroup(session, conversation, myUserId);
    }

    // A group exists. Catch up on anything missed — that may be the very Welcome that lets us in.
    // Best effort: a device that already holds the group must not be broken by a failed fetch.
    await _catchUp(session, conversation.id, state.groupId).catchError((_) {});

    if (!await session.hasGroup(state.groupId)) {
      // The group exists and this device is not in it: a new phone, storage evicted, a member added
      // while we were offline.
      //
      // JOIN IT BY EXTERNAL COMMIT — add our own leaf, with no Welcome and no member's help. This is
      // the whole point of the mechanism: a new device opens a chat whose group already exists and is
      // in it a round trip later, non-destructively, whether or not anyone else is online. See
      // docs/external-join.md.
      final joined = await _tryExternalJoin(
        session,
        conversation,
        state.groupId,
      );
      if (joined != null) {
        _waitingSince.remove(conversation.id);
        return joined;
      }

      // External join was not possible — nobody has published GroupInfo for this group yet (an old
      // group from before this feature, or one whose only members are offline and never refreshed it).
      // Fall back to the original path: announce, and rebuild only as a genuine last resort.
      await _announceDevice(conversation.id);

      // ...unless nobody is coming. Every device that held this group can have lost its key material,
      // and nothing says that cannot happen to both people in the same week. Then no member is left who
      // can admit anybody. So after long enough that a member who WAS coming would have, retire the
      // group and start a new one — safe because it destroys nothing: the old group is remembered, not
      // deleted, and anyone still holding it can still read every message ever sent to it.
      if (_stuckFor(conversation.id) > _stuckAfter) {
        _waitingSince.remove(conversation.id);
        await _repo.mlsResetGroup(conversation.id).catchError((_) {
          return MLSGroupState(groupId: '', epoch: 0);
        });
        return _settleGroup(conversation, myUserId);
      }
      return null;
    }
    _waitingSince.remove(conversation.id);

    // WE HOLD THE GROUP. Nothing below this line may take that away.
    //
    // Reconciliation is hygiene — admitting somebody's new device, pruning a ghost. It talks to the
    // network and builds Commits, so it can fail: a stale key package, a timeout, a Commit refused for
    // a reason we did not anticipate. None of that says anything about whether THIS device can read
    // THIS conversation, and it must not be allowed to imply otherwise.
    //
    // It was allowed to, once. An exception here propagated out, the chat route caught it and left the
    // group id empty, and because decryption is gated on that id the device rendered every message as
    // unavailable, refused to send, and reported that encryption was still being set up — while
    // holding perfectly good keys the whole time. A failure to tidy up bricked a working conversation.
    await _reconcileDevices(
      session,
      conversation.id,
      state.groupId,
    ).catchError((_) {});

    // Leave fresh GroupInfo behind, so the NEXT new device to open this chat can external-join at the
    // current epoch instead of waiting to be admitted. FIRE AND FORGET: it is best-effort housekeeping
    // for someone else's future, and awaiting a network call here once hung the user's own send.
    unawaited(
      _publishGroupInfo(
        session,
        conversation.id,
        state.groupId,
      ).catchError((_) {}),
    );
    return state.groupId;
  }

  /// Builds the conversation's group: one leaf per DEVICE of every member, all in a single Commit so
  /// nobody lands an epoch behind.
  ///
  /// The group id is minted here and claimed through the server's compare-and-set, so it can only ever
  /// be set once. If we lose that race, the group we just built is an orphan: throw it away and join
  /// the real one.
  Future<String?> _establishGroup(
    MlsSession session,
    Conversation conversation,
    String myUserId,
  ) async {
    final published = await _repo.mlsDevices(conversation.id);
    final targets = _missingDevices(session, published, const []);

    // Nobody else is reachable. For a direct chat that is the whole conversation, and the user needs
    // to be told plainly rather than watching a message fail to send.
    final reachableOthers = targets.any((d) => d.userId != myUserId);
    if (conversation.kind == ConversationKind.direct && !reachableOthers) {
      throw const PeerKeysMissingException();
    }
    if (targets.isEmpty) {
      // Not even another device of our own to add. There is no Commit to make, so there is nothing to
      // establish: a group of one has nobody to talk to.
      throw const PeerKeysMissingException();
    }

    final claimed = await _claimFor(conversation.id, targets);
    final groupId = newUuid();

    await session.createGroup(groupId);
    final outcome = await session.commitAdd(
      groupId,
      claimed.map((c) => c.keyPackage).toList(),
      _offer(conversation.id, groupId),
    );

    if (outcome == CommitOutcome.conflict) {
      // Another device of ours established the group first. What we built is an orphan nobody will
      // ever join — drop it and settle again, joining theirs.
      await session.discardGroup(groupId);
      return _settleGroup(conversation, myUserId);
    }

    // Publish GroupInfo straight away, so a member added here who is not online to take the Welcome
    // can external-join the moment they open the chat instead of waiting. Fire and forget.
    unawaited(
      _publishGroupInfo(session, conversation.id, groupId).catchError((_) {}),
    );
    return groupId;
  }

  /// Brings the group's leaves into line with who is actually in the conversation and what devices
  /// they actually have: adds the missing, prunes what should not be there.
  ///
  /// This is the heart of it. Leaves and membership drift apart constantly — someone signs in on a
  /// laptop, someone is added, someone's storage is evicted — and reconciling them is what keeps every
  /// device able to read. Safe to run from any member, as often as we like: the compare-and-set picks
  /// one winner and the losers simply find nothing left to do.
  ///
  /// The membership is re-read from the SERVER, never taken from a Conversation the caller happens to
  /// be holding. Not defensiveness — the fix for a real bug: when a member was added, every other
  /// member's live-event handler ran this with the conversation it had fetched BEFORE the add, so the
  /// newcomer looked like a stranger and the first member to react promptly removed them again.
  Future<void> _reconcileDevices(
    MlsSession session,
    String conversationId,
    String groupId,
  ) async {
    // Not while a call is up. Every add and prune here is a Commit, a Commit moves the epoch, and the
    // epoch is what the call's key is derived from — so reconciling mid-call would pull the key out
    // from under a conversation two people are having right now, to admit a device that can perfectly
    // well wait thirty seconds. Nothing is lost by deferring: the device has announced itself, and it
    // is admitted the moment the call ends.
    if (_callsInProgress > 0) return;

    // Leaves we would prune but are not allowed to (we are not an admin). Remembered so the loop does
    // not keep retrying the same refusal instead of getting on with the adds.
    final refused = <String>[];

    for (var attempt = 0; attempt < _commitAttempts; attempt++) {
      final members = await _repo.listConversationMembers(conversationId);
      final memberIds = members.map((m) => m.userId).toSet();
      final published = await _repo.mlsDevices(conversationId);
      final leaves = await session.memberIdentities(groupId);

      // Prune first — a leaf that should not be there must not still be in the group when we go and
      // encrypt to it.
      //
      // Unless we are not allowed to: in a group, only an admin removes anybody. A non-admin is
      // refused, and that is fine — pruning a departed member or a ghost is hygiene, and it can wait
      // for an admin. What must NOT happen is the refusal stopping us ADDING the devices that are
      // missing, which is the part somebody is actually waiting on.
      final stale = refused.isNotEmpty
          ? const <String>[]
          : _staleLeaves(session, leaves, memberIds, published);

      if (stale.isNotEmpty) {
        final CommitOutcome outcome;
        try {
          outcome = await session.commitRemoveDevices(
            groupId,
            stale,
            _offer(
              conversationId,
              groupId,
              // A legacy leaf has no ':' and IS the user id whole; userOf() on it is empty,
              // which would declare the removal of nobody.
              removes: stale
                  .map((l) => userOf(l).isEmpty ? l : userOf(l))
                  .toSet()
                  .toList(),
            ),
          );
        } on ApiException catch (e) {
          if (e.statusCode != 403) rethrow;
          refused.addAll(stale); // not ours to remove; get on with the adds
          continue;
        }
        if (outcome == CommitOutcome.conflict) {
          await _catchUp(session, conversationId, groupId);
        }
        continue; // look again either way: the group has changed shape
      }

      final missing = _missingDevices(session, published, leaves);
      if (missing.isEmpty) return;

      final claimed = await _claimFor(conversationId, missing);
      if (claimed.isEmpty) return; // they published nothing after all

      final outcome = await session.commitAdd(
        groupId,
        claimed.map((c) => c.keyPackage).toList(),
        _offer(conversationId, groupId),
      );
      if (outcome == CommitOutcome.accepted) {
        // Verify each add actually MATERIALISED: the leaf a claimed package produces answers
        // to whatever credential is inside the bytes, and a stale directory entry can carry
        // somebody's long-dead legacy identity. A device that is still not a leaf after its
        // own accepted Add can never be added by this route — remember that, or the next
        // reconcile claims the same package and commits the same no-op forever.
        final now = (await session.memberIdentities(groupId)).toSet();
        final duds = claimed
            .where((c) => !now.contains(deviceIdentity(c.userId, c.deviceId)))
            .toList();
        if (duds.isEmpty) return;
        for (final dud in duds) {
          _zombieDevices.add(deviceIdentity(dud.userId, dud.deviceId));
          // Our own dead device's packages we can actually purge — only the owner may —
          // which stops every OTHER member's reconcile walking into the same trap.
          if (dud.userId == session.userId) {
            unawaited(_repo.deleteKeyPackages(dud.deviceId).catchError((_) {}));
          }
        }
        continue; // the prune round removes whatever leaf the dud package created
      }

      // Refused: another member committed first. Apply their Commit and look again — the device we
      // were adding may already be in, in which case there is nothing left to do.
      await _catchUp(session, conversationId, groupId);
    }
  }

  /// Offers a staged Commit to the server. Returns whether it became the group's next epoch.
  Future<bool> Function({
    required int baseEpoch,
    required Uint8List commit,
    Uint8List? welcome,
  })
  _offer(
    String conversationId,
    String groupId, {
    List<String> removes = const [],
  }) {
    return ({
      required int baseEpoch,
      required Uint8List commit,
      Uint8List? welcome,
    }) async {
      final result = await _repo.mlsCommit(
        conversationId,
        groupId: groupId,
        baseEpoch: baseEpoch,
        commit: commit,
        welcome: welcome,
        // Declared so the server can hold a Commit to the same rule as the roster: in a group, only an
        // admin removes anybody else. It cannot read the Commit to check, so this is the honest path
        // declaring itself.
        removes: removes,
      );
      return result.accepted;
    };
  }

  /// Leaves with no business being in the group. Two kinds, pruned differently.
  ///
  ///   * A DEPARTED MEMBER — every leaf they hold goes, so their phone AND their laptop, not whichever
  ///     one the group found first.
  ///   * A GHOST DEVICE — a member who is staying, but one of whose devices no longer exists: its key
  ///     packages are gone from the directory because that client was cleared and came back with a new
  ///     identity. Only that leaf goes. Pruning it by user would take the person's live phone with it.
  ///
  /// A user with no published devices at all is left alone: that is what a member who has never opened
  /// Pheme looks like, and also what one looks like for the instant between purging a dead identity's
  /// key packages and publishing the new one's.
  List<String> _staleLeaves(
    MlsSession session,
    List<String> leaves,
    Set<String> memberIds,
    Map<String, List<String>> published,
  ) {
    final out = <String>[];
    for (final leaf in leaves) {
      if (leaf == session.identity) continue; // never prune ourselves
      final userId = userOf(leaf);
      if (userId.isEmpty) {
        // A LEGACY leaf — an identity from before leaves carried a device id, so it names a
        // person and no device. No current client can hold its keys, so it can never read
        // anything and never leaves on its own. Prune it deliberately; leaving it be keeps a
        // dead leaf in every member's tree forever.
        out.add(leaf);
        continue;
      }

      if (!memberIds.contains(userId)) {
        out.add(leaf); // departed member
        continue;
      }
      final devices = published[userId] ?? const <String>[];
      if (devices.isEmpty) continue; // cannot tell; leave them be
      if (!devices.contains(deviceOf(leaf))) out.add(leaf); // ghost device
    }
    return out;
  }

  /// Published devices that are not already leaves, excluding our own — ours holds the group (or is
  /// creating it), and claiming our own key package would burn one for nothing.
  List<MLSDeviceRef> _missingDevices(
    MlsSession session,
    Map<String, List<String>> published,
    List<String> leaves,
  ) {
    final have = leaves.toSet();
    final out = <MLSDeviceRef>[];
    published.forEach((userId, deviceIds) {
      for (final deviceId in deviceIds) {
        final identity = deviceIdentity(userId, deviceId);
        if (identity == session.identity) continue;
        if (have.contains(identity)) continue;
        if (_zombieDevices.contains(identity)) continue;
        out.add(MLSDeviceRef(userId: userId, deviceId: deviceId));
      }
    });
    return out;
  }

  /// Devices whose claimed key package turned out to be a TRAP: it was committed, and the leaf it
  /// produced does not answer to `userId:deviceId` at all — a package published under a device id
  /// whose credential inside is a long-dead legacy identity. Adding it creates a leaf nobody can
  /// hold, the device stays "missing", and every reconcile does it again: half of an add/prune war.
  /// Remembered so this session never claims for them again; a zombie of OUR OWN user additionally
  /// gets its published packages purged, which ends it for everyone.
  final _zombieDevices = <String>{};

  /// Claims one key package per device, keeping WHICH device each was claimed for. A device that
  /// has published none is skipped.
  Future<List<MLSClaimedKeyPackage>> _claimFor(
    String conversationId,
    List<MLSDeviceRef> devices,
  ) async {
    try {
      return await _repo.claimKeyPackages(conversationId, devices);
    } on ApiException catch (e) {
      if (e.statusCode == 404) return const [];
      rethrow;
    }
  }

  /// Applies every Commit the group has made since this device last looked, in order.
  ///
  /// Without this, a device that was closed while the group changed can never decrypt again: MLS will
  /// not let it read the new epoch until it has applied the Commit that created it, and that Commit
  /// may be far outside the page of history the chat view loads.
  Future<void> _catchUp(
    MlsSession session,
    String conversationId,
    String groupId,
  ) async {
    final from = await session.epoch(groupId);
    final messages = await _repo.mlsCommitsSince(conversationId, from);

    for (final msg in messages) {
      if (msg.contentType == ContentType.mlsWelcome) {
        // Only one of these is addressed to this device; the rest fail harmlessly, without touching
        // our key packages (a Welcome names the exact package it is for).
        if (!await session.hasGroup(groupId)) {
          await session.tryJoin(groupId, msg.ciphertext);
        }
        continue;
      }
      if (msg.contentType == ContentType.mlsCommit) {
        await session
            .applyCommit(groupId, msg.ciphertext, msg.mlsEpoch ?? 0)
            .catchError((_) {
              // A Commit we cannot apply is from a branch we are not on, or one we already have.
              // Neither is fixed by retrying, and neither should stop us applying the rest.
            });
      }
    }
  }

  /// Tells the conversation that this device holds no group and needs to be let in.
  ///
  /// Carries no key material — it is a request to be added, not a Welcome — so, unlike the message it
  /// replaces, it cannot be used to destroy anybody's key packages.
  Future<void> _announceDevice(String conversationId) async {
    await _repo
        .sendChatMessage(
          conversationId,
          Uint8List.fromList([1]),
          ContentType.mlsDevice,
        )
        .catchError((_) {
          // Best effort. The next open announces again, and any member who opens the chat reconciles
          // and finds this device missing regardless.
          return ChatMessage(
            id: '',
            conversationId: conversationId,
            senderId: '',
            ciphertext: Uint8List(0),
            contentType: ContentType.mlsDevice,
            createdAt: '',
          );
        });
  }

  Duration _stuckFor(String conversationId) {
    final since = _waitingSince[conversationId];
    if (since == null) {
      _waitingSince[conversationId] = DateTime.now();
      return Duration.zero;
    }
    return DateTime.now().difference(since);
  }

  /// Joins the current group by external commit. Returns the group id on success, or null when it is
  /// not possible — no GroupInfo has been published, or the GroupInfo is for a group that is no longer
  /// current — in which case the caller falls back to announcing itself.
  Future<String?> _tryExternalJoin(
    MlsSession session,
    Conversation conversation,
    String groupId,
  ) async {
    for (var attempt = 0; attempt < _externalJoinAttempts; attempt++) {
      final info = await _repo
          .mlsGroupInfo(conversation.id)
          .catchError((_) => null);
      // No material to join against, or it is for a group that has since been replaced. Either way,
      // external join cannot help here.
      if (info == null || info.groupId != groupId) return null;

      try {
        final outcome = await session.joinByExternalCommit(
          groupId,
          info.groupInfo,
          info.epoch,
          _offer(conversation.id, groupId),
        );
        if (outcome == CommitOutcome.accepted) {
          _rememberReadable(conversation.id, groupId);
          // The epoch just moved; leave fresh GroupInfo for the next joiner. Fire and forget.
          unawaited(
            _publishGroupInfo(
              session,
              conversation.id,
              groupId,
            ).catchError((_) {}),
          );
          return groupId;
        }
        // Conflict: a Commit landed between fetching the GroupInfo and offering ours. Refetch newer
        // GroupInfo and try again.
      } on Object {
        // Building or offering the external commit failed for a reason a retry will not fix.
        return null;
      }
    }
    return null;
  }

  /// Exports fresh GroupInfo for a group this device holds and uploads it, so a future new device can
  /// external-join at the current epoch.
  Future<void> _publishGroupInfo(
    MlsSession session,
    String conversationId,
    String groupId,
  ) async {
    final groupInfo = await session.exportGroupInfo(groupId);
    final epoch = await session.epoch(groupId);
    await _repo.publishGroupInfo(
      conversationId,
      groupId: groupId,
      epoch: epoch,
      groupInfo: groupInfo,
    );
  }

  void _rememberReadable(String conversationId, String groupId) {
    final groups = _readableGroups[conversationId] ?? const <String>[];
    final all = [groupId, ...groups.where((g) => g != groupId)];
    _readableGroups[conversationId] = all;

    // On disk too, so the next open of this chat asks the server nothing. Best effort: a failure here
    // costs one round trip next time, not correctness.
    unawaited(_store.rememberGroupIds(conversationId, all).catchError((_) {}));
  }

  // --- membership ------------------------------------------------------------------------------

  /// Responds to another device announcing itself: add it, if we hold the group.
  ///
  /// Every member with the app open does this. They race, and that is fine — the compare-and-set lets
  /// exactly one Commit through and the others find the device already added and stop.
  Future<void> admitAnnouncedDevice(
    String conversationId,
    String myUserId,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) return;
    await _reconcileDevices(session, conversationId, state.groupId);
  }

  /// Adds a user to a group: server membership FIRST, then every one of their devices as its own leaf.
  ///
  /// It has to be that way round, because the key directory is scoped to a conversation: we cannot ask
  /// which devices someone has until they are in it. If it turns out they have none — they have never
  /// opened Pheme — take them back off the roster, so an admin is told they could not be reached
  /// rather than left with a member who can never read anything.
  Future<void> addGroupMember(
    String conversationId,
    String myUserId,
    String newUserId,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      throw const NotInGroupException();
    }

    await _repo.addConversationMember(conversationId, newUserId);

    final devices = await _repo.mlsDevices(conversationId);
    if ((devices[newUserId] ?? const []).isEmpty) {
      await _repo
          .removeConversationMember(conversationId, newUserId)
          .catchError((_) {});
      throw const PeerKeysMissingException();
    }

    await _reconcileDevices(session, conversationId, state.groupId);
  }

  /// Removes a user from a group. The MLS Commit goes FIRST — that is what actually cuts them off —
  /// and it removes EVERY device they have. Only then is their server membership dropped.
  Future<void> removeGroupMember(
    String conversationId,
    String myUserId,
    String memberUserId,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);

    if (memberUserId == myUserId) {
      // Leaving. MLS forbids committing your own removal, so this is not a Commit at all: drop the
      // membership, and destroy the group state on this device so nothing here can read the
      // conversation any more. The members who remain prune the leaves we leave behind on their next
      // reconcile.
      await _repo.removeConversationMember(conversationId, memberUserId);
      if (state.isEstablished) await session.discardGroup(state.groupId);
      _readableGroups.remove(conversationId);
      return;
    }

    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      throw const NotInGroupException();
    }

    for (var attempt = 0; attempt < _commitAttempts; attempt++) {
      final outcome = await session.commitRemoveUsers(state.groupId, [
        memberUserId,
      ], _offer(conversationId, state.groupId, removes: [memberUserId]));
      if (outcome == CommitOutcome.accepted) break;

      // Somebody else moved the group. Catch up and remove them from the epoch that actually happened
      // — they must not be left in it.
      await _catchUp(session, conversationId, state.groupId);
      if (attempt == _commitAttempts - 1) {
        throw Exception(
          'could not remove that member — the group changed underneath us',
        );
      }
    }

    await _repo.removeConversationMember(conversationId, memberUserId);
  }

  // --- messages --------------------------------------------------------------------------------

  /// Encrypts and sends a message, and caches its body — because we will never be able to read it
  /// back. MLS destroys the message key on encrypt.
  Future<ChatMessage> sendMessage(
    Conversation conversation,
    String myUserId,
    String body, {
    String? replyTo,
    List<Uint8List> photos = const [],
  }) async {
    final session = await this.session(myUserId);
    final groupId = await ensureGroup(conversation, myUserId);
    if (groupId == null) throw const NotInGroupException();

    // The photos go up FIRST, and each gets a fresh key.
    //
    // The order matters: the message is what carries the keys, so a message posted before its photos
    // exist would reference blobs nobody can fetch. Upload first, and if an upload fails the message
    // is simply never sent — better than a bubble with a permanent hole in it.
    final attachments = <ChatPhoto>[];
    for (final photo in photos) {
      attachments.add(await _uploadPhoto(conversation.id, photo));
    }

    final content = ChatContent(
      body: body,
      replyTo: replyTo,
      photos: attachments,
    );

    final ciphertext = await session.encrypt(
      groupId,
      serializeContent(content),
    );
    final message = await _repo.sendChatMessage(
      conversation.id,
      ciphertext,
      ContentType.application,
    );

    await _cache.cacheContent(conversation.id, message.id, content);
    return message;
  }

  /// Seals one photo, uploads the ciphertext, and returns the reference that goes inside the message.
  ///
  /// The key is minted here and travels ONLY in the return value — which the caller puts inside the
  /// MLS-encrypted content. It is never sent to the server, never logged, and never stored anywhere
  /// but the message. That is the entire difference between an encrypted photo and a photo in a
  /// database.
  Future<ChatPhoto> _uploadPhoto(
    String conversationId,
    Uint8List plaintext,
  ) async {
    final size = await decodeImageSize(plaintext);
    final sealed = await sealPhoto(plaintext);

    final id = await _repo.uploadAttachment(conversationId, sealed.bytes);
    if (id.isEmpty) throw Exception('could not upload that photo');

    return ChatPhoto(
      id: id,
      key: base64Encode(sealed.key),
      width: size.width,
      height: size.height,
      mime: 'image/jpeg',
      size: plaintext.length,
    );
  }

  /// Fetches and opens one photo. The bytes are ready to draw.
  ///
  /// Unlike a message, a photo CAN be fetched twice — the blob stays on the server and the key stays
  /// in the message, so nothing is consumed by reading it. That is why photos are cached for speed
  /// rather than for correctness, which is the opposite of everything else here.
  Future<Uint8List> fetchPhoto(String conversationId, ChatPhoto photo) async {
    final sealed = await _repo.downloadAttachment(conversationId, photo.id);
    return openPhoto(keyBase64: photo.key, sealed: sealed);
  }

  /// Decrypts a message against whichever of the conversation's groups it belongs to, and caches the
  /// body on first — and only — sight.
  ///
  /// Returns the cached body when we have already read it, and null when this device cannot read it at
  /// all. Null is a real answer, not a failure: MLS gives a device no access to what was said before
  /// it joined.
  Future<ChatContent?> decryptMessage(
    String conversationId,
    String myUserId,
    ChatMessage message,
  ) async {
    final cached = _cache.content(conversationId, message.id);
    if (cached != null) return cached;

    final groups = _readableGroups[conversationId];
    if (groups == null || groups.isEmpty) return null;

    final session = await this.session(myUserId);
    final plaintext = await session.decryptAny(groups, message.ciphertext);
    if (plaintext == null) return null;

    final content = parseContent(plaintext);
    await _cache.cacheContent(conversationId, message.id, content);
    return content;
  }

  /// Posts the record of a call into the conversation, encrypted like anything else.
  ///
  /// Only the caller does this, and only for a call that was never answered — so exactly one message
  /// is written, by the one device that knows the call rang out. It is a real message: the other end
  /// reads it from its own history, on every device, forever. Nothing about it is a UI flourish.
  Future<void> postCallEvent(
    String conversationId,
    String myUserId,
    String outcome,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) return;

    final content = ChatContent(body: '{"outcome":"$outcome"}');
    final ciphertext = await session.encrypt(
      state.groupId,
      serializeContent(content),
    );
    final message = await _repo.sendChatMessage(
      conversationId,
      ciphertext,
      ContentType.callEvent,
    );

    // Straight into the cache: we will never be able to decrypt it. Without this the caller — the one
    // person who knows the call went unanswered — would see its own record of it sealed.
    await _cache.cacheContent(conversationId, message.id, content);
  }

  // --- calls -----------------------------------------------------------------------------------

  /// The key a given DEVICE seals its call signalling with, and the epoch it was derived at.
  ///
  /// Keyed per sending device, not per call: all of a person's devices would otherwise seal under one
  /// key with independently chosen nonces, and an AES-GCM nonce collision between two of them leaks
  /// the authentication key. This removes the possibility rather than trusting 96 random bits not to
  /// repeat.
  ///
  /// Every member device can derive any sender's key — that is how they open it — so this gives GROUP
  /// authenticity, not SENDER authenticity. Between two people that is meaningless. It would not be
  /// sound for group calls without also signing the payload.
  Future<ExportedSecret?> callKeyFor(
    String conversationId,
    String myUserId,
    String callId,
    String senderIdentity,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished) return null;

    return session.exportSecret(
      state.groupId,
      callKeyLabel,
      Uint8List.fromList('$callId|$senderIdentity'.codeUnits),
      32,
    );
  }

  /// Brings this device to the group's current epoch. A caller MUST do this before deriving a call
  /// key.
  ///
  /// The exporter only exports from the CURRENT epoch. A device that is behind — someone's phone was
  /// admitted an hour ago and nothing made this one notice — would seal its invite at an epoch its
  /// peer has already left. The peer cannot go back to it, so it simply cannot read the invite, and
  /// the call rings out with no way to say why.
  Future<int> catchUpToLatest(String conversationId, String myUserId) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished) return 0;
    await _catchUp(session, conversationId, state.groupId);
    return session.epoch(state.groupId);
  }

  /// Brings this device up to [epoch] so it can derive a key minted there. A device that is behind can
  /// catch up; one that is AHEAD cannot go back, and must say so instead.
  Future<int> catchUpToEpoch(
    String conversationId,
    String myUserId,
    int epoch,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished) return 0;

    final current = await session.epoch(state.groupId);
    if (current >= epoch) return current;

    await _catchUp(session, conversationId, state.groupId);
    return session.epoch(state.groupId);
  }

  /// Holds the group's membership still for the duration of a call.
  ///
  /// Reconciliation adds a newly signed-in device, which is a Commit, and a Commit moves the epoch —
  /// which moves the call key out from under a call that is already ringing. The change is not urgent:
  /// the new device is admitted the moment the call ends.
  ///
  /// The returned release is idempotent, because callers run it in finally blocks.
  void Function() freezeGroupForCall() {
    _callsInProgress++;
    var released = false;
    return () {
      if (released) return;
      released = true;
      _callsInProgress = _callsInProgress > 0 ? _callsInProgress - 1 : 0;
    };
  }

  /// This device's MLS identity — who a call signal is from.
  Future<String> myIdentity(String myUserId) async =>
      (await session(myUserId)).identity;

  // --- safety numbers --------------------------------------------------------------------------

  /// The digits two people compare, or '' before the group exists.
  Future<String> conversationSafetyNumber(
    String conversationId,
    String myUserId,
  ) async {
    final session = await this.session(myUserId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      return '';
    }
    return session.safetyNumber(state.groupId);
  }

  // --- backup and recovery ---------------------------------------------------------------------

  /// Whether this device must restore before it can have an identity: it holds no keys, the user has
  /// not chosen to start over, and the server has a backup waiting.
  ///
  /// A check that FAILS is not a check that says "no backup". Treating a flaky network as "safe to
  /// mint an identity" is how a real backup gets orphaned by a throwaway one — and publishing key
  /// packages cannot be undone. Fail closed: refuse to decide.
  Future<bool> _needsRestore(String userId) async {
    final cached = _restoreNeeded;
    if (cached != null) return cached;

    final owned =
        await _store.owner() == userId && await _store.readState() != null;
    if (owned || await _store.freshAccepted()) {
      return _restoreNeeded = false;
    }
    return _restoreNeeded = await _repo.getKeyBackup() != null;
  }

  /// Whether the server holds a backup for the signed-in user.
  Future<bool> backupExists() async => await _repo.getKeyBackup() != null;

  /// Whether this device already holds keys.
  Future<bool> hasLocalKeys() async => await _store.readState() != null;

  /// Records that the user chose to start over rather than restore. Their existing encrypted history
  /// stays unreadable here until they do restore; new conversations work from a fresh identity.
  Future<void> acceptFreshIdentity() async {
    await _store.acceptFresh();
    _restoreNeeded = false;
  }

  /// Seals this device's state under [passphrase] and uploads it.
  Future<void> backupKeys(String userId, String passphrase) async {
    final session = await this.session(userId);
    final state = await session.exportState();
    if (state == null) throw Exception('no local key state to back up');

    final blob = await rust.mlsBackupEncrypt(
      passphrase: Uint8List.fromList(passphrase.codeUnits),
      plaintext: state,
    );
    await _repo.putKeyBackup(
      session.deviceId,
      salt: blob.salt,
      nonce: blob.nonce,
      ciphertext: blob.ciphertext,
    );
  }

  /// Recovers this device's state from the server backup.
  ///
  /// Must run before the first [session] call, so a fresh identity is not minted in place of the
  /// recovered one. Returns false when there is no backup; throws on a wrong passphrase (the GCM tag
  /// fails).
  Future<bool> restoreKeys(String userId, String passphrase) async {
    final backup = await _repo.getKeyBackup();
    if (backup == null) return false;

    // Another session may have set up an identity while the prompt sat open. Restoring would replace
    // it with an older snapshot and strand whatever was said in between.
    if (await _store.readState() != null && await _store.owner() == userId) {
      throw const IdentityAlreadySetUpException();
    }

    final state = await rust.mlsBackupDecrypt(
      passphrase: Uint8List.fromList(passphrase.codeUnits),
      salt: backup.salt,
      nonce: backup.nonce,
      ciphertext: backup.ciphertext,
    );

    // KILL EVERY LIVE SESSION BEFORE TOUCHING THE RUST CLIENT, not after.
    //
    // There is one Rust client per process, and mlsLoad replaces it. A session's operations queue up
    // behind its own mutex and check assertLive() before they run — but they check the GENERATION, and
    // if the generation is still the old one when the client underneath has already been swapped, a
    // queued operation sails through the check and then runs against the wrong client entirely. It
    // would export that client's state and persist it under the old session's identity: one identity's
    // key material, written to disk under another's name.
    //
    // Bumping first closes the window. Anything queued is refused; anything already inside a Rust call
    // completes against the client it started with, because Rust's own mutex is still holding it.
    _store.invalidate();
    _session = null;
    _sessionUserId = '';
    _settling.clear();
    _readableGroups.clear();

    // Validate that the recovered blob really is a client state before committing to it — and read
    // back WHICH DEVICE it belongs to.
    await rust.mlsLoad(state: state);
    final restoredDevice = deviceOf(await rust.mlsIdentity());

    await _store.writeState(state);
    await _store.setOwner(userId);

    // Adopt the identity we just restored. The groups inside that state hold leaves under the ORIGINAL
    // device's name, and its published key packages are filed under it — so this device has to answer
    // to that name, not to one of its own. Keeping a local id here would leave the restored client
    // unable to be added to anything: publishing keys as one device and holding leaves as another.
    if (restoredDevice.isNotEmpty) {
      await saveMlsDeviceId(_storage, restoredDevice, namespace: _ns);
    }

    _restoreNeeded = false;
    return true;
  }

  /// Erases this device's keys and every decrypted body. Logout.
  ///
  /// The key state and the plaintext cache are exactly what the encryption exists to protect, and
  /// leaving them readable on a shared device after signing out would defeat it. There is no way to
  /// re-derive them afterwards except from the passphrase-protected backup — which is the point of
  /// that backup.
  Future<void> wipeLocalKeys() async {
    // Same ordering as restoreKeys, and for the same reason: a session whose operations are queued
    // behind its mutex must be refused BEFORE the client they are queued against is unloaded, or one
    // of them wakes up, passes the liveness check on a stale generation, and writes the keys the user
    // just asked us to destroy straight back to disk.
    _store.invalidate();
    _session = null;
    _sessionUserId = '';
    _restoreNeeded = null;
    _settling.clear();
    _readableGroups.clear();
    _waitingSince.clear();

    await rust.mlsUnload();
    await _store.wipe();
    await _cache.wipe();
    await clearMlsDeviceId(_storage, namespace: _ns);
  }
}
