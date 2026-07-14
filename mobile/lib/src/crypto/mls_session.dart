// One device's MLS session: the lock, and the persist-after-every-mutation rule.
//
// The Rust side already serialises access to the ratchet, so why a lock here as well? Because the
// unit that has to be atomic is not the Rust call — it is the Rust call AND the disk write that
// follows it. Every mutating call hands back the new state to persist. Two of them interleaving
// would run in a defined order inside Rust and then race on the way to disk, and the older state
// could land last: a ratchet that has moved on, saved as one that has not, which is every message
// after that point permanently unreadable.
//
// So: one operation at a time, from the Rust call through to the file being written.
//
// The web client needs a cross-TAB lock for the same reason and a version counter to detect another
// tab moving the state on. Neither has an analogue here — there is one process and one isolate — so
// they are gone. What remains is the generation counter, which does the job the web's key-material
// epoch does: a logout or a restore replaces the keys, and a session still holding the old ones must
// be refused rather than allowed to write them back over what replaced them.

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import '../rust/api/mls.dart' as rust;
import 'mls_device.dart';
import 'mls_errors.dart';
import 'mls_store.dart';

/// Whether a Commit the server was offered became the group's next epoch.
enum CommitOutcome { accepted, conflict }

/// A secret exported from the group, together with the epoch it came from.
///
/// The two travel together because the exporter is per-epoch and the caller MUST pin the pair. Read
/// separately, a Commit landing in between would hand back a key from one epoch labelled with
/// another — and the two ends of a call would talk past each other with no error anywhere.
class ExportedSecret {
  const ExportedSecret({required this.secret, required this.epoch});

  final Uint8List secret;
  final int epoch;
}

/// THE ONE RUST CLIENT, AND THE ONE QUEUE THAT GUARDS IT.
///
/// There is exactly one MLS client per process — the Rust side makes that structural, and it is right,
/// because one device has one ratchet. What was NOT guarded is that a client can be swapped: load a
/// different state blob and the same client is now a different device.
///
/// So the queue is process-wide, not per-session. It has to be. A per-session lock serialises each
/// session against itself and leaves them free to interleave with each other — and every interleaving
/// is a session issuing calls against somebody else's ratchet:
///
///   * `_commit` holds its lock across the network round trip that offers the Commit to the server.
///     Another session claiming the client in that window means `commitAccepted` lands on the WRONG
///     client — accepting one device's Commit into another device's state.
///   * `load()` mints an identity and exports it. Racing that against another session's operation
///     exports whatever client happened to be installed, so a device can persist — and then publish
///     key packages for — an identity that is not its own. That is not theoretical: it is what this
///     queue was written for. The group refused the second device with DuplicateSignatureKey, because
///     its key package carried the first device's signature key.
///
/// In the app there is one session and this queue is a formality. It is what makes a two-device test
/// possible in one process, which is the only way to exercise the group choreography — establish,
/// welcome, commit, admit — against a real server.
Future<void> _clientTail = Future<void>.value();

/// Which session's state the client is currently holding.
int _clientOwner = -1;
int _nextSessionTag = 0;

/// Runs [op] with the one Rust client to itself, from the first call through to the last write.
Future<T> _withClient<T>(Future<T> Function() op) {
  final result = _clientTail.then((_) => op());
  // The tail must never carry an error forward: a failed operation is the caller's problem, but a tail
  // that completed with an error would skip every callback chained after it — so every later call
  // would wait forever for a turn that never comes. Swallow it here; the caller still sees it via
  // [result].
  _clientTail = result.then((_) {}, onError: (Object _) {});
  return result;
}

class MlsSession {
  MlsSession._(this._store, this.userId, this.deviceId, this._generationTag)
    : _tag = _nextSessionTag++;

  final MlsStore _store;
  final String userId;
  final String deviceId;
  final int _generationTag;

  /// Identifies this session's claim on the one Rust client.
  final int _tag;

  /// The last state we persisted, so we can put the Rust client back the way we left it if another
  /// session has since loaded its own.
  Uint8List? _blob;

  /// This device's leaf identity, as it appears in the group.
  String get identity => deviceIdentity(userId, deviceId);

  /// Runs [op] with exclusive access to the MLS state — the Rust call AND the disk write that follows
  /// it, which is the unit that has to be atomic.
  ///
  /// Liveness is checked before the operation and not merely before the write: an encrypt on a
  /// destroyed identity still consumes ratchet state, and there is no reason to run it at all.
  Future<T> _exclusive<T>(Future<T> Function() op) {
    return _withClient(() async {
      _assertLive();
      await _claimClient();
      return op();
    });
  }

  /// Makes sure the one Rust client is holding OUR state before we ask it to do anything.
  ///
  /// Almost always a no-op: in a running app there is one session and it already owns the client.
  Future<void> _claimClient() async {
    if (_clientOwner == _tag) return;

    final blob = _blob;
    if (blob == null) {
      // We have never persisted, so there is nothing to restore. That only happens before load()
      // finishes, and load() installs the client itself.
      _clientOwner = _tag;
      return;
    }

    await rust.mlsLoad(state: blob);
    _clientOwner = _tag;
  }

  void _assertLive() {
    if (_store.generation != _generationTag) {
      throw const SessionInvalidatedException();
    }
  }

  /// Persists the state a mutating call handed back. Must be called before its result is acted on.
  Future<void> _persist(Uint8List state) async {
    // Remember it in memory as well as on disk: this is what lets us put the client back if another
    // session borrows it.
    _blob = state;
    await _store.writeState(state);
  }

  // --- lifecycle -------------------------------------------------------------------------------

  /// Loads this device's session, minting an identity if it has none.
  ///
  /// Runs inside the client queue like every other Rust call. Minting installs a brand new identity in
  /// the one client and immediately exports it; if another session's operation is allowed to interleave
  /// there, what gets exported is whatever client is installed at that instant — and the device
  /// persists, and then publishes key packages for, an identity that is not its own.
  static Future<MlsSession> load({
    required MlsStore store,
    required String userId,
    required String? storedDeviceId,
    required Future<void> Function(String deviceId) rememberDeviceId,
    required bool mustRestore,
  }) {
    return _withClient(
      () => _load(
        store: store,
        userId: userId,
        storedDeviceId: storedDeviceId,
        rememberDeviceId: rememberDeviceId,
        mustRestore: mustRestore,
      ),
    );
  }

  static Future<MlsSession> _load({
    required MlsStore store,
    required String userId,
    required String? storedDeviceId,
    required Future<void> Function(String deviceId) rememberDeviceId,
    required bool mustRestore,
  }) async {
    // State belonging to a different account must never be adopted. Encrypting under someone else's
    // MLS identity would send their key material out under our name.
    if (await store.owner() != userId) {
      await store.wipe();
      store.invalidate();
    }

    final saved = await store.readState();

    // No local keys and a backup waiting: do NOT mint an identity. Publishing key packages cannot be
    // undone, and a peer claiming one would send a Welcome that the restore is about to make
    // unopenable — a message stuck forever. Refuse, and let the user decide.
    if (saved == null && mustRestore) throw const NeedsRestoreException();

    String deviceId;
    var retiredDeviceId = '';

    if (saved != null) {
      await rust.mlsLoad(state: saved);

      // The device id comes from the CLIENT, not from storage. The credential is what the groups
      // actually hold a leaf under, and a restored backup carries the identity of the device it was
      // taken FROM — so storage can be wrong about this and the credential cannot.
      deviceId = deviceOf(await rust.mlsIdentity());

      if (deviceId.isEmpty) {
        // A legacy identity, naming a person rather than a device. It cannot be kept: every group it
        // holds has this user occupying one leaf shared across all their devices, which is the bug
        // itself. Discard it and mint a proper one. Nothing of value is lost that was not already
        // lost, and the plaintext already read is in the body cache, which is left alone.
        retiredDeviceId = storedDeviceId ?? '';
        deviceId = newUuid();
        await rememberDeviceId(deviceId);
        await _persistNew(store, userId, deviceId);
      } else {
        await rememberDeviceId(deviceId);
      }
    } else {
      // A FRESH identity gets a FRESH device id, even though this device may already have one.
      //
      // A new identity means the old private keys are gone. But the groups this device used to be in
      // still hold a leaf under the old name, and that leaf's keys no longer exist anywhere. Reusing
      // the name would make the new client indistinguishable from the dead one: every member
      // reconciling would see `user:device` already present, conclude there was nothing to add, and
      // never let this device back in. Locked out permanently, by its own name.
      retiredDeviceId = storedDeviceId ?? '';
      deviceId = newUuid();
      await rememberDeviceId(deviceId);
      await _persistNew(store, userId, deviceId);
    }

    await store.setOwner(userId);

    final session = MlsSession._(store, userId, deviceId, store.generation);
    session._retiredDeviceId = retiredDeviceId;

    // The client is now holding THIS session's state — load() is what put it there. Claim it, and
    // remember the blob so we can put it back if another session ever borrows the client.
    session._blob = await store.readState();
    _clientOwner = session._tag;

    return session;
  }

  static Future<void> _persistNew(
    MlsStore store,
    String userId,
    String deviceId,
  ) async {
    final state = await rust.mlsCreate(userId: userId, deviceId: deviceId);
    await store.writeState(state);
  }

  /// A device id this session replaces, whose key packages are now ghosts. Purged by the caller.
  String _retiredDeviceId = '';
  String get retiredDeviceId => _retiredDeviceId;

  // --- key packages ----------------------------------------------------------------------------

  /// Mints [count] single-use key packages, and optionally the one reusable last-resort package.
  ///
  /// Building a key package stores its private half, so this mutates and must persist — a published
  /// package whose private key was never saved is one nobody can ever add us with.
  Future<({List<Uint8List> packages, Uint8List? lastResort})> mintKeyPackages({
    required int count,
    required bool lastResort,
  }) {
    return _exclusive(() async {
      final packages = <Uint8List>[];
      Uint8List? reusable;
      Uint8List? state;

      for (var i = 0; i < count; i++) {
        final out = await rust.mlsKeyPackage();
        packages.add(out.bytes);
        state = out.state;
      }
      if (lastResort) {
        final out = await rust.mlsLastResortKeyPackage();
        reusable = out.bytes;
        state = out.state;
      }

      if (state != null) await _persist(state);
      return (packages: packages, lastResort: reusable);
    });
  }

  // --- groups ----------------------------------------------------------------------------------

  Future<bool> hasGroup(String groupId) =>
      _exclusive(() => rust.mlsHasGroup(groupId: _gid(groupId)));

  /// The group's current epoch, or 0 when this device does not hold it.
  Future<int> epoch(String groupId) =>
      _exclusive(() => _epochUnlocked(groupId));

  Future<int> _epochUnlocked(String groupId) async {
    final gid = _gid(groupId);
    if (!await rust.mlsHasGroup(groupId: gid)) return 0;
    return (await rust.mlsEpoch(groupId: gid)).toInt();
  }

  /// Every leaf's `userId:deviceId`. Empty when this device does not hold the group.
  Future<List<String>> memberIdentities(String groupId) {
    return _exclusive(() async {
      final gid = _gid(groupId);
      if (!await rust.mlsHasGroup(groupId: gid)) return <String>[];
      return rust.mlsMemberIdentities(groupId: gid);
    });
  }

  /// Creates the group locally. It is not real until the server accepts our first Commit.
  Future<void> createGroup(String groupId) {
    return _exclusive(() async {
      final out = await rust.mlsCreateGroup(groupId: _gid(groupId));
      await _persist(out.state);
    });
  }

  /// Discards a group we built but the server never accepted — we lost the race to establish it, and
  /// what we have is an orphan nobody will ever join.
  ///
  /// Never call this on a group other members are in: discarding a live group throws away the key
  /// material for every message ever sent to it, for everyone.
  Future<void> discardGroup(String groupId) {
    return _exclusive(() async {
      final out = await rust.mlsDeleteGroup(groupId: _gid(groupId));
      await _persist(out.state);
    });
  }

  /// Joins from a relayed Welcome. False for a Welcome that is not ours.
  Future<bool> tryJoin(String groupId, Uint8List welcome) {
    return _exclusive(() async {
      final gid = _gid(groupId);
      if (await rust.mlsHasGroup(groupId: gid)) return true;
      try {
        final out = await rust.mlsJoinFromWelcome(welcome: welcome);
        await _persist(out.state);
        return rust.mlsHasGroup(groupId: gid);
      } on Object {
        // Addressed to another device — every device gets its own Welcome — or already used.
        // Neither is worth surfacing.
        return false;
      }
    });
  }

  /// Applies a Commit another member produced, if we are actually behind it.
  ///
  /// [atEpoch] is the epoch the Commit produces. Applying one we are already past is not merely
  /// wasted work: OpenMLS rejects it, and treating that as a failure would make the routine live
  /// echo of a Commit we already hold look like a broken group.
  Future<void> applyCommit(String groupId, Uint8List commit, int atEpoch) {
    return _exclusive(() async {
      final gid = _gid(groupId);
      if (!await rust.mlsHasGroup(groupId: gid)) return;
      if (atEpoch > 0 && atEpoch <= await _epochUnlocked(groupId)) return;
      final out = await rust.mlsApplyCommit(groupId: gid, commit: commit);
      await _persist(out.state);
    });
  }

  // --- commits ---------------------------------------------------------------------------------

  /// Stages a Commit, offers it to the server, and applies it ONLY if the server takes it.
  ///
  /// The whole round trip is inside the lock, network call and all — the one place this file holds
  /// the lock across I/O. A staged Commit lives in the persisted state, so a second operation
  /// staging its own on top would corrupt both. Membership changes are rare; one blocked round trip
  /// is a fair price for a group that cannot fork.
  ///
  /// On refusal the staged Commit is thrown away and never applied. Applying a Commit the group
  /// refused is precisely what forks a device off for good: it advances its own ratchet to an epoch
  /// nobody else is in, and silently stops being able to read anything, forever.
  Future<CommitOutcome> _commit(
    String groupId,
    Future<({Uint8List commit, Uint8List? welcome, Uint8List state})> Function()
    stage,
    Future<bool> Function({
      required int baseEpoch,
      required Uint8List commit,
      Uint8List? welcome,
    })
    offer,
  ) {
    return _exclusive(() async {
      final gid = _gid(groupId);
      final baseEpoch = await _epochUnlocked(groupId);

      final staged = await stage();
      // Staging writes a pending commit into the state. It must be persisted, or a restart would
      // leave the group with a commit the server has accepted and this device has forgotten.
      await _persist(staged.state);

      bool accepted;
      try {
        accepted = await offer(
          baseEpoch: baseEpoch,
          commit: staged.commit,
          welcome: staged.welcome,
        );
      } on Object {
        // Refused, or never got there. Either way this Commit is not the group's history, so it
        // must not become ours.
        final out = await rust.mlsCommitRejected(groupId: gid);
        await _persist(out.state);
        rethrow;
      }

      if (!accepted) {
        final out = await rust.mlsCommitRejected(groupId: gid);
        await _persist(out.state);
        return CommitOutcome.conflict;
      }

      final out = await rust.mlsCommitAccepted(groupId: gid);
      await _persist(out.state);
      return CommitOutcome.accepted;
    });
  }

  /// Adds devices to the group, each as its own leaf, in a single Commit so nobody lands an epoch
  /// behind. One Welcome covers all of them.
  Future<CommitOutcome> commitAdd(
    String groupId,
    List<Uint8List> keyPackages,
    Future<bool> Function({
      required int baseEpoch,
      required Uint8List commit,
      Uint8List? welcome,
    })
    offer,
  ) {
    return _commit(groupId, () async {
      final out = await rust.mlsStageAdd(
        groupId: _gid(groupId),
        keyPackages: keyPackages,
      );
      return (commit: out.commit, welcome: out.welcome, state: out.state);
    }, offer);
  }

  /// Removes every leaf belonging to each user, in one Commit — throwing someone out. Every device
  /// they have, not whichever one the tree happened to find first.
  Future<CommitOutcome> commitRemoveUsers(
    String groupId,
    List<String> userIds,
    Future<bool> Function({
      required int baseEpoch,
      required Uint8List commit,
      Uint8List? welcome,
    })
    offer,
  ) {
    return _commit(groupId, () async {
      final out = await rust.mlsStageRemoveUsers(
        groupId: _gid(groupId),
        userIds: userIds,
      );
      return (commit: out.bytes, welcome: null, state: out.state);
    }, offer);
  }

  /// Removes the exact leaves named — pruning a ghost device while leaving that same person's live
  /// phone alone. Removing by user would take the phone out with it.
  Future<CommitOutcome> commitRemoveDevices(
    String groupId,
    List<String> identities,
    Future<bool> Function({
      required int baseEpoch,
      required Uint8List commit,
      Uint8List? welcome,
    })
    offer,
  ) {
    return _commit(groupId, () async {
      final out = await rust.mlsStageRemoveDevices(
        groupId: _gid(groupId),
        identities: identities,
      );
      return (commit: out.bytes, welcome: null, state: out.state);
    }, offer);
  }

  // --- messages --------------------------------------------------------------------------------

  Future<Uint8List> encrypt(String groupId, Uint8List plaintext) {
    return _exclusive(() async {
      final out = await rust.mlsEncrypt(
        groupId: _gid(groupId),
        plaintext: plaintext,
      );
      await _persist(out.state);
      return out.bytes;
    });
  }

  /// Decrypts an application message, or null for a control message.
  Future<Uint8List?> decrypt(String groupId, Uint8List ciphertext) {
    return _exclusive(() async {
      final out = await rust.mlsDecrypt(
        groupId: _gid(groupId),
        ciphertext: ciphertext,
      );
      await _persist(out.state);
      return out.plaintext;
    });
  }

  /// Decrypts against whichever of the conversation's groups the message actually belongs to.
  ///
  /// A conversation can have had more than one group: when every device holding one lost its key
  /// material, the only way to talk again was to start fresh. The old group is retired, not deleted,
  /// and a message from before the reset is still perfectly readable by a device that still holds it.
  Future<Uint8List?> decryptAny(
    List<String> groupIds,
    Uint8List ciphertext,
  ) async {
    for (final groupId in groupIds) {
      try {
        final out = await decrypt(groupId, ciphertext);
        if (out != null) return out;
      } on SessionInvalidatedException {
        rethrow; // the keys are gone; trying another group will not help
      } on Object {
        // Not this group's message, or not one we can read. Try the next.
      }
    }
    return null;
  }

  /// Derives a secret from the group for something outside MLS's own messaging, with the epoch it
  /// came from. Null when this device does not hold the group.
  ///
  /// A pure read — no ratchet mutation, nothing to persist. That is exactly why call signalling uses
  /// this rather than MLS application messages: deriving a call key must not churn the ratchet.
  Future<ExportedSecret?> exportSecret(
    String groupId,
    String label,
    Uint8List context,
    int length,
  ) {
    return _exclusive(() async {
      final gid = _gid(groupId);
      if (!await rust.mlsHasGroup(groupId: gid)) return null;
      final secret = await rust.mlsExportSecret(
        groupId: gid,
        label: label,
        context: context,
        length: BigInt.from(length),
      );
      return ExportedSecret(
        secret: secret,
        epoch: await _epochUnlocked(groupId),
      );
    });
  }

  /// The digits two people compare out of band to prove nobody is in the middle. Computed from the
  /// group's own ratchet tree, so a key package the server swapped in shows up as different digits.
  Future<String> safetyNumber(String groupId) =>
      _exclusive(() => rust.mlsSafetyNumber(groupId: _gid(groupId)));

  /// The whole client state, for sealing into a backup.
  Future<Uint8List?> exportState() => _store.readState();
}

/// The server hands the group id out as an opaque string; MLS wants bytes.
Uint8List _gid(String groupId) => Uint8List.fromList(utf8.encode(groupId));
