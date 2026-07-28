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

import 'package:flutter/foundation.dart';
import 'dart:io' show Platform;

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../core/api_exception.dart';
import '../data/pheme_repository.dart';
import '../models/chat_models.dart';
import '../rust/api/mls.dart' as rust;
import 'chat_cache.dart';
import 'chat_content.dart';
import 'image_size.dart';
import 'photo_crypto.dart';
import 'catch_up_gate.dart';
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

/// The exporter label for history-sync keys. Versioned like the call label, and identical to the
/// web client's so a sealed transcript is readable across platforms.
const _historySyncLabel = 'pheme/history-sync/v1';

/// A human label for THIS device, for the user's "your devices" list. Best effort — a device is
/// perfectly usable if the label is vague. Nothing identifying beyond the OS family.
String deviceLabel() {
  if (Platform.isIOS) return 'Pheme on iOS';
  if (Platform.isAndroid) return 'Pheme on Android';
  if (Platform.isMacOS) return 'Pheme on macOS';
  if (Platform.isWindows) return 'Pheme on Windows';
  if (Platform.isLinux) return 'Pheme on Linux';
  return 'Pheme';
}

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
    this.onIdentityMinted,
  }) : _repo = repository,
       _storage = storage,
       _ns = namespace,
       _store = MlsStore(storage, namespace: namespace);

  /// Called just after this device writes an MLS device id it did not have before.
  ///
  /// The push registration wants to know. A device registers for push when the app starts, which on
  /// a fresh install is BEFORE any identity exists, so the address the server stores carries no
  /// mlsDeviceId — and the server will not hand ciphertext to an address it cannot trace to an MLS
  /// device, because such a row survives revocation. The result is a phone that shows "New message"
  /// and never the message, with nothing anywhere reporting a fault.
  ///
  /// device_controller already detects the mismatch, but only in the launch check — so the repair
  /// landed on the NEXT launch, and until the user happened to restart the app the previews they had
  /// asked for silently did not work. This closes that window: the moment the identity exists, the
  /// server is told.
  ///
  /// Deliberately a plain callback rather than a dependency on the push layer, which this service
  /// knows nothing about and should not start knowing about.
  final void Function()? onIdentityMinted;

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

  /// Fetched once from the server: the home domain that qualifies this host's MLS
  /// credentials. Best-effort — a failure leaves the 'local' default.
  Future<void>? _homeDomainReady;

  Future<void> _ensureHomeDomain() {
    return _homeDomainReady ??= _repo
        .homeDomain()
        .then((d) {
          setHomeDomain(d);
        })
        .catchError((_) {
          // Leave the default; do not cache the failure so a later load can retry.
          _homeDomainReady = null;
        });
  }

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

  /// The recovery secret this session is unlocked with, held only in memory. It is what auto-backup
  /// re-seals under; null means auto-backup is dormant (no secret to seal with). Set when a backup is
  /// created, restored, or re-unlocked from the locally-stored code on relaunch — never persisted here
  /// (the code itself lives in secure storage; see mls_device.dart).
  String? _sessionPassphrase;

  /// The pending debounced auto-backup, and the user it is for. One timer at a time: a burst of
  /// messages coalesces into a single re-seal.
  Timer? _autoBackupTimer;
  String _autoBackupUser = '';

  /// How long auto-backup waits after a change before re-sealing, so a burst of messages costs one
  /// backup, not one per message.
  static const _autoBackupDebounce = Duration(seconds: 20);

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
    // Learn the home domain before minting any credential, so this device's
    // identity is qualified by the right host. Best-effort: a failure leaves the
    // 'local' default, correct for a non-federated host.
    await _ensureHomeDomain();
    final session = await MlsSession.load(
      store: _store,
      userId: userId,
      storedDeviceId: await loadMlsDeviceId(_storage, namespace: _ns),
      // Fires on a mint AND on a re-mint after a wipe or restore — every case where the id the
      // push registration was told about may no longer be the one this device holds.
      rememberDeviceId: (id) async {
        await saveMlsDeviceId(_storage, id, namespace: _ns);
        onIdentityMinted?.call();
      },
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

    // Register this device on every load, and refresh its last-seen — independent of KeyPackage
    // replenishment, which only publishes when stock runs low. Without this, a long-lived,
    // well-stocked device never appears in "your devices".
    await _repo
        .registerDevice(session.deviceId, deviceLabel())
        .catchError((_) {});

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
      label: deviceLabel(),
    );
  }

  /// Terminates one of the user's OWN devices — the "remove this device" action.
  ///
  /// The device being removed is another leaf of the same user, present in every conversation that
  /// user is in. Only a group member can author the Commit that removes it, so THIS device does it:
  /// for each conversation it holds the group for, it commits away the target's leaf. Then the server
  /// finishes the parts it owns — deletes the target's key packages so it can never be re-added,
  /// revokes its login, and forgets it. The removed device is left with no leaf and a dead token.
  ///
  /// Best-effort per conversation: one that will not commit must not stop the others or the
  /// server-side revocation — a co-member prunes any leaf we could not, and the key-package delete
  /// already stops a rejoin.
  Future<void> terminateOwnDevice(String userId, String deviceId) async {
    final session = await this.session(userId);
    final target = deviceIdentity(userId, deviceId);
    if (target == session.identity) {
      // Terminating the device you are using is just signing out; there is no other device to
      // orchestrate the removal, and committing your own leaf away is not allowed.
      throw Exception('use log out to sign out of this device');
    }

    final conversations = await _repo.listConversations().catchError(
      (_) => <Conversation>[],
    );
    for (final conversation in conversations) {
      try {
        final state = await _repo.mlsGroupState(conversation.id);
        if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
          continue;
        }
        final leaves = await session.memberIdentities(state.groupId);
        if (!leaves.contains(target)) continue;
        for (var attempt = 0; attempt < _commitAttempts; attempt++) {
          final outcome = await session.commitRemoveDevices(
            state.groupId,
            [target],
            // The removed leaf belongs to this same user, so the roster removal the server checks
            // against is of ourselves.
            _offer(conversation.id, state.groupId, removes: [userId]),
          );
          if (outcome == CommitOutcome.accepted) break;
          await _catchUp(session, conversation.id, state.groupId);
        }
      } on Object {
        // This conversation would not settle; leave its leaf for a co-member to prune. The
        // server-side key-package delete below still prevents the device from being re-added.
      }
    }

    // The parts only the server can do: kill the login, delete the key packages, forget the device.
    await _repo.terminateDevice(deviceId);
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
        // We just joined and hold none of the past — ask a co-member for it. A no-op if we have it.
        unawaited(
          requestHistory(conversation.id, myUserId)
              .then((_) => collectPendingHistory(conversation.id, myUserId))
              .catchError((Object _) => false),
        );
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

    // Holding the group is not the same as being able to FOLLOW it. If the catch-up above
    // left this device behind the server's epoch, its ratchet may have forked — see
    // _observeWedge for what happens next.
    if (await _stillBehind(session, conversation.id, state)) {
      _observeWedge(conversation.id, myUserId);
    }

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
    // If we hold the group but none of its history (a device admitted by Welcome), ask for it.
    unawaited(
      requestHistory(conversation.id, myUserId)
          .then((_) => collectPendingHistory(conversation.id, myUserId))
          .catchError((Object _) => false),
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
    final published = (await _repo.mlsDevices(conversation.id)).published;
    final localTargets = _missingDevices(session, published, const []);
    // Members on other hosts are not in the local directory; claim them from their
    // home host (the server routes it). Empty in a single-host deployment.
    final members = await _repo.listConversationMembers(conversation.id);
    final targets = [
      ...localTargets,
      ...remoteMemberRefs(
        members.map((m) => (userId: m.userId, domain: m.domain)),
        const [],
        myUserId,
      ).map((r) => MLSDeviceRef(userId: r.userId, deviceId: '')),
    ];

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
      final directory = await _repo.mlsDevices(conversationId);
      final revoked = directory.revoked;
      // Never ADD back a device that has been revoked. Its KeyPackages are deleted on termination,
      // so normally it is not here at all — but that delete is one step of several, and a revoked
      // device that still had a claimable package would otherwise be welcomed straight back in by
      // the very reconciliation meant to be removing it.
      final published = <String, List<String>>{
        for (final e in directory.published.entries)
          e.key: e.value
              .where((id) => !(revoked[e.key] ?? const <String>[]).contains(id))
              .toList(),
      };
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
          : _staleLeaves(session, leaves, memberIds, published, revoked);

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
      // Remote members are not in the local directory; add those without a leaf yet by
      // claiming from their home host. domainByUser lets an added leaf be matched under
      // the member's OWN domain, not ours. Both are empty single-host.
      final memberDomains = members.map(
        (m) => (userId: m.userId, domain: m.domain),
      );
      final domainByUser = domainsByUser(memberDomains);
      final remote = remoteMemberRefs(
        memberDomains,
        leaves,
        session.userId,
      ).map((r) => MLSDeviceRef(userId: r.userId, deviceId: ''));
      final toAdd = [...missing, ...remote];
      if (toAdd.isEmpty) return;

      final claimed = await _claimFor(conversationId, toAdd);
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
        // reconcile claims the same package and commits the same no-op forever. A remote
        // member's leaf answers to its own home domain, so match under that.
        final now = (await session.memberIdentities(groupId)).toSet();
        String idOf(MLSClaimedKeyPackage c) =>
            deviceIdentity(c.userId, c.deviceId, domainByUser[c.userId]);
        final duds = claimed.where((c) => !now.contains(idOf(c))).toList();
        if (duds.isEmpty) return;
        for (final dud in duds) {
          _zombieDevices.add(idOf(dud));
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
    Map<String, List<String>> revoked,
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
      // A REVOKED device, said so by the server. Checked BEFORE the bail below: terminating a
      // device deletes its KeyPackages, so a revoked device has none published and would otherwise
      // be waved through as "cannot tell" — which is how a device whose access had just been
      // removed kept its leaf, and with it everything sent afterwards. Deleting every device on an
      // account made that certain rather than merely likely.
      if ((revoked[userId] ?? const <String>[]).contains(deviceOf(leaf))) {
        out.add(leaf);
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

  /// One catch-up per conversation for a whole pass of failed decrypts.
  ///
  /// A feed opens with fifty messages and asks about each in turn. Without this, fifty unreadable
  /// ones would each fetch the Commit history — fifty round trips to learn the same thing once. See
  /// [CatchUpGate], which holds the coalescing and the retry gap and is tested on its own.
  final _decryptGate = CatchUpGate();

  Future<void> _catchUpForDecrypt(String conversationId, String myUserId) {
    return _decryptGate.run(conversationId, () async {
      final session = await this.session(myUserId);
      final state = await _repo.mlsGroupState(conversationId);
      if (!state.isEstablished) return;
      await _catchUp(session, conversationId, state.groupId);
    });
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
        await session.applyCommit(groupId, msg.ciphertext, msg.mlsEpoch ?? 0).catchError((
          Object e,
        ) {
          // A Commit we cannot apply is from a branch we are not on, or one we already have.
          // Neither is fixed by retrying, and neither should stop us applying the rest.
          //
          // SAID OUT LOUD, though. This swallow is how a forked device — one that holds the
          // group but can no longer apply anybody's Commits — stays silent while every message
          // after the fork renders as unreadable. It took a database dump to see it last time.
          // One line in the log is the difference between "the app is broken" and a diagnosis.
          debugPrint(
            'pheme/mls: commit at epoch ${msg.mlsEpoch} not applied for '
            '$conversationId ($groupId): $e',
          );
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

    // The server accepts a bare id, a mimi:// identity, or a `username@host` handle
    // and returns the member it actually created — so key the rest of this on the
    // RESOLVED id, not on whatever spelling the caller typed.
    final added = await _repo.addConversationMember(conversationId, newUserId);

    // A remote member's devices live on their home host, claimed during reconcile,
    // not in this host's published directory — so the "no devices yet, roll back"
    // check is only meaningful for a local member. A remote one goes straight to
    // reconcile, the same path a freshly-opened cross-host group takes.
    if (added.domain.isEmpty) {
      final devices = (await _repo.mlsDevices(conversationId)).published;
      if ((devices[added.userId] ?? const []).isEmpty) {
        await _repo
            .removeConversationMember(conversationId, added.userId)
            .catchError((_) {});
        throw const PeerKeysMissingException();
      }
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
      // The crate matches removal targets against qualified credentials, so the
      // target is the qualified user key; the server-side `removes` claim stays
      // the bare user id, which is what conversation membership is keyed by.
      final outcome = await session.commitRemoveUsers(state.groupId, [
        userKey(memberUserId),
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
    // A newly-sent body is only on this device until it is backed up; carry it into the backup.
    autoBackupSoon(_sessionUserId);
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
    var plaintext = await session.decryptAny(groups, message.ciphertext);

    // A failed decrypt has TWO causes and they are not the same thing.
    //
    //   * The message predates this device's membership. Permanent, and correct to say so.
    //   * WE ARE BEHIND. The Commit that carried the group to the epoch this message was sealed at
    //     has not been applied here yet, so the key to open it does not exist on this device — YET.
    //
    // The second was being reported as the first. A sender that commits and then immediately sends —
    // which is exactly what happens when a device joins, or rejoins after signing back in — produces
    // messages the other end cannot open for the second or two before its own catch-up lands. Those
    // messages were marked unreadable, permanently, and nothing ever looked again: the bubble says
    // "Not available on this device" for a message whose key was about to arrive.
    //
    // So catch up and ask once more. MLS keeps MAX_PAST_EPOCHS (32) behind the head, so a message a
    // few epochs old opens perfectly well once the Commits in front of it are applied.
    if (plaintext == null) {
      await _catchUpForDecrypt(conversationId, myUserId);
      plaintext = await session.decryptAny(
        _readableGroups[conversationId] ?? groups,
        message.ciphertext,
      );
    }
    if (plaintext == null) return null;

    final content = parseContent(plaintext);
    await _cache.cacheContent(conversationId, message.id, content);
    // The decrypted body now lives only here (the message key is gone); back it up.
    autoBackupSoon(_sessionUserId);
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
    autoBackupSoon(_sessionUserId);
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
    final epoch = await session.epoch(state.groupId);
    // Caught up and still behind: the fingerprint of a forked ratchet. Put the conversation
    // under observation — the recheck decides, and heals if it is real.
    if (epoch < state.epoch && await session.hasGroup(state.groupId)) {
      _observeWedge(conversationId, myUserId);
    }
    return epoch;
  }

  // --- fork self-healing ---------------------------------------------------------------
  //
  // A device can hold a group and still be unable to FOLLOW it. If its ratchet ever forked,
  // every Commit the others make is rejected from then on — silently: the device keeps its
  // group id, so nothing announces, nothing resets, and nothing ever recovers. It just stops
  // being able to read anything new, forever, while looking joined.
  //
  // The fingerprint is unmistakable: the server's epoch is ahead, every Commit that would
  // close the gap is fetchable, and applying them changes nothing. A device that is merely
  // behind closes the gap the moment a catch-up runs; a forked one cannot, ever.
  //
  // A settle or live catch-up that leaves the device still behind puts the conversation
  // under observation, and a fresh check after a grace period decides. Still behind then →
  // the group is beyond following and is retired — which destroys nothing: the old group is
  // remembered and stays readable, and a fresh one comes up that everyone can actually be
  // in. Every wedged member does the same; the server's compare-and-set lets one through.

  static const _wedgeGrace = Duration(seconds: 20);

  /// Conversations under observation for a forked ratchet, by id → the pending recheck.
  final _wedgeTimers = <String, Timer>{};

  /// True when this device holds the group, the server's epoch is ahead, and catching up
  /// does not close the gap — the one state an intact ratchet can never be in afterwards.
  Future<bool> _stillBehind(
    MlsSession session,
    String conversationId,
    MLSGroupState state,
  ) async {
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      return false;
    }
    if (await session.epoch(state.groupId) >= state.epoch) return false;
    await _catchUp(session, conversationId, state.groupId).catchError((_) {});
    return await session.epoch(state.groupId) < state.epoch;
  }

  /// Puts a conversation under observation; the recheck heals it if the wedge is real.
  void _observeWedge(String conversationId, String myUserId) {
    if (_wedgeTimers.containsKey(conversationId)) return;
    _wedgeTimers[conversationId] = Timer(_wedgeGrace, () {
      unawaited(() async {
        try {
          final session = await this.session(myUserId);
          final state = await _repo.mlsGroupState(conversationId);
          if (!await _stillBehind(session, conversationId, state)) return;
          // Confirmed: this ratchet can never follow the group again. Retire it and
          // settle into whatever comes next.
          await _repo.mlsResetGroup(conversationId);
          final conversation = await _repo.getConversation(conversationId);
          await ensureGroup(conversation, myUserId);
        } on Object {
          // Nothing lost — the next settle of this conversation observes again.
        } finally {
          _wedgeTimers.remove(conversationId);
        }
      }());
    });
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

  /// Whether the user already chose to start fresh on this device — so the restore gate does not nag
  /// them to recover a backup they declined.
  Future<bool> hasAcceptedFresh() => _store.freshAccepted();

  /// Restores from a secret typed by the user, tolerating a loosely-entered recovery code.
  ///
  /// Tries the input verbatim first (a legacy passphrase, or a code already canonical), then its
  /// normalised form (the code with dashes/spaces stripped and ambiguous letters folded). restoreKeys
  /// validates before any side effect, so a first attempt that fails on the GCM tag leaves nothing to
  /// undo. A wrong secret throws; [IdentityAlreadySetUpException] propagates unchanged.
  Future<bool> restoreWithSecret(String userId, String input) async {
    try {
      return await restoreKeys(userId, input);
    } on IdentityAlreadySetUpException {
      rethrow;
    } on Object {
      final normalized = normalizeRecoveryCode(input);
      if (normalized == input) rethrow; // nothing new to try
      return restoreKeys(userId, normalized);
    }
  }

  /// Seals this device's key state AND its message transcript under [passphrase] and uploads them.
  ///
  /// Two independent seals under one secret: the key state (what lets a restored device decrypt new
  /// messages) and the transcript (the decrypted bodies — the only copy, since MLS destroys the
  /// message key on decrypt). Without the transcript a restored device would come up able to follow
  /// the conversation forward but showing a blank history. Also arms auto-backup with the secret, so
  /// later messages are re-sealed without the user lifting a finger.
  Future<void> backupKeys(String userId, String passphrase) async {
    final session = await this.session(userId);
    final state = await session.exportState();
    if (state == null) throw Exception('no local key state to back up');

    // NORMALISED, and restoreKeys normalises to match. Sealing and opening must agree on the exact
    // bytes, and the code shown to the user is not those bytes: it is grouped with dashes for
    // legibility, which normalisation strips.
    final passphraseBytes = Uint8List.fromList(
      normalizeRecoveryCode(passphrase).codeUnits,
    );
    final stateBlob = await rust.mlsBackupEncrypt(
      passphrase: passphraseBytes,
      plaintext: state,
    );

    // The transcript, sealed under its own salt/nonce. Empty when this device has read nothing yet —
    // then there is simply no transcript blob to send.
    final bodies = await _cache.exportAllContents();
    ({Uint8List salt, Uint8List nonce, Uint8List ciphertext})? transcript;
    if (bodies.isNotEmpty) {
      final plaintext = Uint8List.fromList(
        utf8.encode(jsonEncode({'v': 1, 'bodies': bodies})),
      );
      final blob = await rust.mlsBackupEncrypt(
        passphrase: passphraseBytes,
        plaintext: plaintext,
      );
      transcript = (
        salt: blob.salt,
        nonce: blob.nonce,
        ciphertext: blob.ciphertext,
      );
    }

    await _repo.putKeyBackup(
      session.deviceId,
      salt: stateBlob.salt,
      nonce: stateBlob.nonce,
      ciphertext: stateBlob.ciphertext,
      transcriptSalt: transcript?.salt,
      transcriptNonce: transcript?.nonce,
      transcriptCiphertext: transcript?.ciphertext,
    );

    // Keep the secret so auto-backup can carry later changes without prompting again.
    _sessionPassphrase = passphrase;
  }

  /// Recovers a user's HISTORY from the server backup, onto a FRESH device identity.
  ///
  /// Must run before the first [session] call. Returns false when there is no backup; throws on a
  /// wrong passphrase (the GCM tag fails).
  ///
  /// It does NOT adopt the backed-up device's identity — it mints a brand-new one. Adopting it (a
  /// "clone") was the parallel-device bug: restore onto a second device while the first is still
  /// live, and both hold the SAME leaf. MLS advances the ratchet per leaf, so each message one sends
  /// makes the other unable to decrypt — the "not available on this device" both people saw. A fresh
  /// leaf, added to each group by external join and reconciliation, is the only correct shape.
  ///
  /// The passphrase's real job here is to open the transcript so the new device shows the old
  /// history; the key-state blob is opened only to VALIDATE the passphrase (a wrong one fails the
  /// GCM tag) and is then discarded, since this device gets its own keys, not the backup's.
  Future<bool> restoreKeys(String userId, String passphrase) async {
    final backup = await _repo.getKeyBackup();
    if (backup == null) return false;

    // Another session may have set up an identity while the prompt sat open. Coming up fresh now
    // would strand it and whatever was said in between.
    if (await _store.readState() != null && await _store.owner() == userId) {
      throw const IdentityAlreadySetUpException();
    }

    // NORMALISED, exactly as backupKeys sealed it. The code handed to the user is grouped with
    // dashes — "ABCDE-FGHIJ-…" — and normalisation strips them, so the raw string could never open
    // a backup this app had made. restoreWithSecret hides that by retrying with the normalised form
    // on failure, which is why it never showed up in the app; doing it right here means the retry
    // is a courtesy for a loosely-typed code rather than the only thing that works.
    final passphraseBytes = Uint8List.fromList(
      normalizeRecoveryCode(passphrase).codeUnits,
    );

    // Prove the passphrase by opening the state blob (throws on a wrong one), then discard it — this
    // device does not adopt those keys.
    await rust.mlsBackupDecrypt(
      passphrase: passphraseBytes,
      salt: backup.salt,
      nonce: backup.nonce,
      ciphertext: backup.ciphertext,
    );

    // Open the transcript too, before minting anything, so a bad blob fails cleanly. Its own seal
    // under the same passphrase. A history that will not open must not fail the restore — the device
    // still comes up working, just without the old scrollback.
    Map<String, Map<String, String>>? bodies;
    if (backup.hasTranscript) {
      try {
        final opened = await rust.mlsBackupDecrypt(
          passphrase: passphraseBytes,
          salt: backup.transcriptSalt!,
          nonce: backup.transcriptNonce!,
          ciphertext: backup.transcriptCiphertext!,
        );
        final parsed = jsonDecode(utf8.decode(opened));
        if (parsed is Map && parsed['bodies'] is Map) {
          bodies = (parsed['bodies'] as Map).map(
            (conversationId, msgs) => MapEntry(
              conversationId as String,
              (msgs as Map).map(
                (id, body) => MapEntry(id as String, body as String),
              ),
            ),
          );
        }
      } on Object {
        // Best effort — the device is already coming up; it just will not have the old history.
      }
    }

    // Come up as a FRESH device. acceptFreshIdentity records the choice so _needsRestore stops
    // demanding a restore, then the next session() call mints a new identity in the cleared store.
    _restoreNeeded = null;
    _session = null;
    _sessionUserId = '';
    _settling.clear();
    _readableGroups.clear();
    await clearMlsDeviceId(_storage, namespace: _ns);
    await acceptFreshIdentity();

    // Mint the fresh identity now, before importing history: loading the session may write to the
    // store, and the history has to sit on top of an established identity, not be overwritten by it.
    await session(userId);

    if (bodies != null) {
      try {
        await _cache.importContents(bodies);
      } on Object {
        // Already up and working without it.
      }
    }

    // Keep the secret so THIS fresh device's own backup stays current, and schedule a catch-up so the
    // imported history is re-sealed under this device's identity rather than only the old one's.
    _sessionPassphrase = passphrase;
    autoBackupSoon(userId);
    return true;
  }

  /// Makes sure this device has a recovery backup, generating a one-time code the first time.
  ///
  /// Returns the freshly generated code to show ONCE (write it down), or null when nothing new needs
  /// showing — either a backup already exists (in which case auto-backup is re-armed from the local
  /// copy of the code, since the in-memory secret is lost on relaunch), or this device must restore
  /// first (that is the restore gate's job, not auto-setup's).
  Future<String?> ensureRecoveryBackup(String userId) async {
    try {
      // A fresh device with a backup waiting throws here — auto-setup is not our call then.
      await session(userId);
    } on Object {
      return null;
    }

    if (await backupExists()) {
      final localCode = await loadRecoveryCode(_storage, namespace: _ns);
      if (localCode != null) {
        _sessionPassphrase = normalizeRecoveryCode(localCode);
        autoBackupSoon(userId);
      }
      return null;
    }
    return _sealUnderNewCode(userId);
  }

  /// Generates a fresh recovery code, seals this device's keys+transcript under it, and stores the
  /// code locally so it can be shown again. Returns the code to display once.
  Future<String> _sealUnderNewCode(String userId) async {
    final code = generateRecoveryCode();
    final secret = normalizeRecoveryCode(code);
    await backupKeys(userId, secret); // sets _sessionPassphrase
    await saveRecoveryCode(_storage, code, namespace: _ns);
    return code;
  }

  /// Re-seals under a BRAND-NEW code, retiring the old one (a "regenerate"). Returns the new code.
  Future<String> regenerateRecoveryCode(String userId) =>
      _sealUnderNewCode(userId);

  /// This device's stored recovery code, for showing it again. Null if none is stored here.
  Future<String?> recoveryCode() => loadRecoveryCode(_storage, namespace: _ns);

  /// This device's MLS device id, for flagging which row in "your devices" is the current one. Null
  /// before an identity exists.
  Future<String?> currentDeviceId() =>
      loadMlsDeviceId(_storage, namespace: _ns);

  // --- history sync ----------------------------------------------------------------------------
  //
  // A device that joins a conversation it holds no transcript for asks a co-member for the history,
  // over the conversation's own MLS group. Any co-member can answer (all members already see all
  // messages, so this leaks nothing): it seals this conversation's bodies under a group-derived key
  // bound to the requester, uploads the blob, and points the requester at it. Degrades to the backup
  // (Phase 3) when nobody is online, and to new-messages-only when there is no backup either.

  /// The current group's member identities, or empty when this device does not hold the group — the
  /// input to the responder election.
  Future<List<String>> groupMemberIdentities(
    String conversationId,
    String userId,
  ) async {
    final session = await this.session(userId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      return const [];
    }
    return session.memberIdentities(state.groupId);
  }

  /// Conversations this device has already asked history for this session — so it asks once, not on
  /// every settle.
  final _historyRequested = <String>{};

  /// Asks co-members for this conversation's pre-join history — once per conversation per session, and
  /// only when this device holds the group but has no local transcript for it (it just joined).
  Future<void> requestHistory(String conversationId, String userId) async {
    if (_historyRequested.contains(conversationId)) return;
    final session = await this.session(userId);
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) return;
    // Nothing to fetch if we already hold this conversation's history.
    final existing = (await _cache.exportAllContents())[conversationId];
    if (existing != null && existing.isNotEmpty) return;

    _historyRequested.add(conversationId);
    final epoch = await session.epoch(state.groupId);
    final body = _encodeControl({'id': session.identity, 'epoch': epoch});
    try {
      await _repo.sendChatMessage(
        conversationId,
        body,
        ContentType.mlsHistoryRequest,
      );
    } on Object {
      _historyRequested.remove(conversationId); // let a later settle try again
    }
  }

  /// Responds to a history request: seal this conversation's transcript under a group-derived key
  /// bound to the requester, upload it, and point the requester at it. No-op if we hold nothing, or
  /// the request is our own device's. Callers elect a single responder before calling this.
  Future<void> offerHistory(
    String conversationId,
    String userId,
    String requesterIdentity,
  ) async {
    final session = await this.session(userId);
    if (requesterIdentity.isEmpty || requesterIdentity == session.identity) {
      debugPrint('Pheme: no history offer — the request is our own device');
      return;
    }
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      debugPrint(
        'Pheme: no history offer — this device does not hold the group '
        '(established=${state.isEstablished})',
      );
      return;
    }
    final bodies = (await _cache.exportAllContents())[conversationId];
    if (bodies == null || bodies.isEmpty) {
      debugPrint(
        'Pheme: no history offer — this device has no transcript to share',
      );
      return;
    }

    final derived = await session.exportSecret(
      state.groupId,
      _historySyncLabel,
      Uint8List.fromList(utf8.encode(requesterIdentity)),
      32,
    );
    if (derived == null) {
      debugPrint(
        'Pheme: no history offer — could not derive the sync key at this epoch',
      );
      return;
    }

    final plaintext = Uint8List.fromList(
      utf8.encode(jsonEncode({'v': 1, 'bodies': bodies})),
    );
    final sealed = await rust.mlsBackupEncrypt(
      passphrase: derived.secret,
      plaintext: plaintext,
    );
    final String historyId;
    try {
      historyId = await _repo.uploadHistory(conversationId, sealed.ciphertext);
    } on Object catch (e) {
      debugPrint(
        'Pheme: no history offer — the sealed blob would not upload: $e',
      );
      return;
    }
    final offer = _encodeControl({
      'to': requesterIdentity,
      'epoch': derived.epoch,
      'historyId': historyId,
      'salt': base64Encode(sealed.salt),
      'nonce': base64Encode(sealed.nonce),
    });
    try {
      await _repo.sendChatMessage(
        conversationId,
        offer,
        ContentType.mlsHistoryOffer,
      );
      debugPrint('Pheme: history offer delivered to $requesterIdentity');
    } on Object catch (e) {
      // The requester will re-ask on its next settle if this never lands.
      debugPrint('Pheme: the history offer was sealed but not delivered: $e');
    }
  }

  /// Looks for an offer already waiting for this device, and opens it.
  ///
  /// requestHistory asks; this collects the answer. Without it the answer had one delivery route —
  /// the live stream, at the instant it was posted — so a device that asked while the co-member
  /// answering was asleep, or that reconnected a moment too late, never saw it. Since the request
  /// is made once per conversation per session, that meant a blank history for the whole session.
  ///
  /// Returns whether any history was imported. Offers addressed to other devices are skipped by
  /// receiveHistoryOffer, which is what makes it safe for every member to see them.
  Future<bool> collectPendingHistory(
    String conversationId,
    String userId,
  ) async {
    final List<ChatMessage> offers;
    try {
      offers = await _repo.listHistoryOffers(conversationId);
    } on Object catch (e) {
      // An older server has no such endpoint; the live path still works.
      debugPrint('Pheme: could not list history offers: $e');
      return false;
    }
    for (final offer in offers) {
      try {
        if (await receiveHistoryOffer(
          conversationId,
          userId,
          offer.ciphertext,
        )) {
          return true;
        }
      } on Object catch (e) {
        debugPrint('Pheme: a pending history offer would not open: $e');
      }
    }
    return false;
  }

  /// Opens a history offer addressed to this device: derive the same group-bound key, fetch the
  /// sealed blob, open it and merge the bodies under what we already hold. Returns whether any
  /// history was imported. Ignores offers not addressed to this device.
  Future<bool> receiveHistoryOffer(
    String conversationId,
    String userId,
    Uint8List offerCiphertext,
  ) async {
    final session = await this.session(userId);
    final offer = _decodeControl(offerCiphertext);
    if (offer == null || offer['to'] != session.identity) return false;
    final state = await _repo.mlsGroupState(conversationId);
    if (!state.isEstablished || !await session.hasGroup(state.groupId)) {
      return false;
    }

    final derived = await session.exportSecret(
      state.groupId,
      _historySyncLabel,
      Uint8List.fromList(utf8.encode(session.identity)),
      32,
    );
    // Bound to the epoch the offer was sealed at; if we have since moved, re-request rather than
    // open with a key that will not match.
    if (derived == null || derived.epoch != (offer['epoch'] as num?)?.toInt()) {
      return false;
    }

    final Uint8List plaintext;
    try {
      final blob = await _repo.getHistory(
        conversationId,
        offer['historyId'] as String? ?? '',
      );
      plaintext = await rust.mlsBackupDecrypt(
        passphrase: derived.secret,
        salt: base64Decode(offer['salt'] as String? ?? ''),
        nonce: base64Decode(offer['nonce'] as String? ?? ''),
        ciphertext: blob,
      );
    } on Object {
      return false;
    }

    try {
      final parsed = jsonDecode(utf8.decode(plaintext));
      if (parsed is Map && parsed['bodies'] is Map) {
        final bodies = (parsed['bodies'] as Map).map(
          (id, body) => MapEntry(id as String, body as String),
        );
        if (bodies.isNotEmpty) {
          await _cache.importContents({conversationId: bodies});
          return true;
        }
      }
    } on Object {
      return false;
    }
    return false;
  }

  /// base64/JSON control-body helpers. The wire carries the ciphertext field base64-encoded, so the
  /// bytes here are the UTF-8 JSON — the exact shape the web client uses, so a request or offer works
  /// across platforms.
  Uint8List _encodeControl(Map<String, dynamic> obj) =>
      Uint8List.fromList(utf8.encode(jsonEncode(obj)));

  Map<String, dynamic>? _decodeControl(Uint8List bytes) {
    try {
      final parsed = jsonDecode(utf8.decode(bytes));
      return parsed is Map<String, dynamic> ? parsed : null;
    } on Object {
      return null;
    }
  }

  /// Schedules a debounced re-seal of this device's backup, if auto-backup is armed (a secret is
  /// held). Fire-and-forget: a failed auto-backup is not worth surfacing — the next change schedules
  /// another, and the keys/transcript are safe locally regardless.
  void autoBackupSoon(String userId) {
    if (_sessionPassphrase == null || userId.isEmpty) return;
    _autoBackupUser = userId;
    if (_autoBackupTimer != null) return;
    _autoBackupTimer = Timer(_autoBackupDebounce, () {
      _autoBackupTimer = null;
      final pass = _sessionPassphrase;
      if (pass == null) return;
      unawaited(backupKeys(_autoBackupUser, pass).catchError((_) {}));
    });
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
    _historyRequested.clear();
    // The two maps this replaced were missed here, which left the retry gap armed across a wipe: an
    // identity that signed out and back in within the gap had its first catch-up suppressed on
    // behalf of the identity that no longer exists.
    _decryptGate.reset();

    // Disarm auto-backup and forget the secret, so nothing re-seals this identity after it is gone.
    _autoBackupTimer?.cancel();
    _autoBackupTimer = null;
    _sessionPassphrase = null;
    _autoBackupUser = '';

    await rust.mlsUnload();
    await _store.wipe();
    await _cache.wipe();
    await clearMlsDeviceId(_storage, namespace: _ns);
    await clearRecoveryCode(_storage, namespace: _ns);
  }
}
