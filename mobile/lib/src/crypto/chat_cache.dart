// The decrypted bodies of messages this device has read.
//
// THIS IS NOT A CACHE IN THE USUAL SENSE — it is the only copy.
//
// MLS gives forward secrecy by destroying the message key as it goes, which has two consequences
// that between them make this file mandatory rather than an optimisation:
//
//   * a message decrypts EXACTLY ONCE. Read it, and the key is gone. Scroll away and back and there
//     is nothing left to decrypt with.
//   * a sender can NEVER decrypt its own message. The key was destroyed on encrypt.
//
// So every body is written here the first (and only) time it is seen, and a sent body is written
// here at send time. Without it the history renders as a column of blanks — including, absurdly,
// everything the user typed themselves.
//
// Sealed at rest under the same data key as the MLS state: this is the plaintext of end-to-end
// encrypted messages, and leaving it lying in the clear would give away exactly what the encryption
// was for.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import '../rust/api/vault.dart';
import 'attribution.dart';
import 'chat_content.dart';

/// How many message bodies a transcript holds, across every conversation in it.
///
/// The number the server's backup guard compares one upload against another by — it cannot open
/// the seal, so this is all it has to tell a device that has read everything from one that has read
/// nothing. Counted in one place because the count and the blob must describe the same thing: a
/// count that overstates lets an empty backup pass the guard, which is the failure it exists to
/// prevent.
int countBodies(Map<String, Map<String, String>> all) =>
    all.values.fold<int>(0, (total, bodies) => total + bodies.length);

/// Raised when a body cannot be written to the cache.
///
/// It is an exception rather than a silent return because of what the caller is holding: a
/// plaintext MLS has already destroyed the key for. Swallowing this loses it for good, and the
/// caller is the only place left that still has it.
class ChatCacheWriteException implements Exception {
  const ChatCacheWriteException(this.conversationId, this.reason);

  final String conversationId;
  final String reason;

  @override
  String toString() => 'ChatCacheWriteException($conversationId): $reason';
}

/// The seal and its inverse, injectable so the durability rules below can be tested without the
/// Rust library — the same split the history-handoff tests already make. Production always uses
/// [vaultSeal] / [vaultOpen]; nothing else in the app passes these.
typedef VaultSealFn =
    Future<Uint8List> Function({
      required String domain,
      required List<int> key,
      required List<int> plaintext,
    });
typedef VaultOpenFn =
    Future<Uint8List> Function({
      required String domain,
      required List<int> key,
      required List<int> sealed,
    });

class ChatCache {
  /// [namespace] must match the MlsStore's — the two share a data key, and in a two-device test each
  /// device needs its own. Empty in the app.
  ///
  /// [seal], [open] and [supportDirectory] exist for tests only. What they make testable is the one
  /// thing in this class that cannot be recovered when it goes wrong: these bodies are the only copy
  /// of a decrypted message, because MLS destroys the key on decrypt.
  ChatCache(
    this._storage, {
    String namespace = '',
    VaultSealFn? seal,
    VaultOpenFn? open,
    Future<Directory> Function()? supportDirectory,
  }) : _ns = namespace,
       _seal = seal ?? vaultSeal,
       _open = open ?? vaultOpen,
       _supportDirectory = supportDirectory ?? getApplicationSupportDirectory;

  final VaultSealFn _seal;
  final VaultOpenFn _open;
  final Future<Directory> Function() _supportDirectory;

  final String _ns;

  /// The same key that seals the MLS state.
  String get _dataKeyKey => 'pheme.mlsDataKey$_ns';

  /// Its OWN domain, bound into the seal. The key store and this cache share a key, so without it a
  /// body cache would open cleanly in the key store's place — handing arbitrary bytes to import_state.
  static const _domain = 'pheme.chat.bodies.v1';
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  final FlutterSecureStorage _storage;

  /// conversationId -> (messageId -> the SERIALISED CONTENT, not just the body).
  ///
  /// The whole content, because a message is not only text: it may carry photos and a reply. Storing
  /// just the body would mean a photo message came back as a bare caption the second time it was
  /// looked at — and there is no second decrypt to recover the rest from.
  final _contents = <String, Map<String, String>>{};

  /// The newest body per conversation, for the conversation-list preview. The list cannot decrypt
  /// anything — it only ever sees ciphertext — so a preview can only come from here.
  final _previews = <String, String>{};

  /// The AUTHENTICATED sender (bare user id) of the message each preview came from, and the id of
  /// that message. Both empty for a preview restored from disk, where only the body was stored.
  final _previewSenders = <String, String>{};
  final _previewIds = <String, String>{};

  /// Conversations whose stored bodies could not be opened this session.
  ///
  /// An unreadable file used to read as an empty one, and the next body cached would flush a map
  /// built on that emptiness — replacing a file holding a whole history with one holding a single
  /// message. A failed read is not evidence that there is nothing there; it is evidence that we
  /// must not write. The file is left exactly as it is until something can open it.
  final _unreadable = <String>{};

  Future<Directory> _dir() async {
    final support = await _supportDirectory();
    final dir = Directory('${support.path}/bodies$_ns');
    if (!await dir.exists()) await dir.create(recursive: true);
    return dir;
  }

  Future<File> _file(String conversationId) async =>
      File('${(await _dir()).path}/$conversationId.json');

  Future<Uint8List?> _dataKey() async {
    final encoded = await _storage.read(
      key: _dataKeyKey,
      iOptions: _iosOptions,
    );
    if (encoded == null) return null;
    return Uint8List.fromList(
      encoded.split(',').map(int.parse).toList(growable: false),
    );
  }

  /// Every message this device has managed to read in a conversation. Empty is a legitimate answer,
  /// not a failure.
  Future<Map<String, String>> load(String conversationId) async {
    final cached = _contents[conversationId];
    if (cached != null) return cached;

    final bodies = <String, String>{};
    final file = await _file(conversationId);
    final key = await _dataKey();

    if (await file.exists() && key != null) {
      try {
        final opened = await _open(
          domain: _domain,
          key: key,
          sealed: await file.readAsBytes(),
        );
        final json = jsonDecode(utf8.decode(opened)) as Map<String, dynamic>;
        json.forEach((id, body) {
          if (body is String) bodies[id] = body;
        });
      } on Object {
        // Unreadable, which is NOT the same as absent — see [_unreadable]. The reader carries on
        // with what it has, so the conversation still opens; the writer is the one that must be
        // stopped, or it replaces a file it could not read.
        _unreadable.add(conversationId);
      }
    } else if (await file.exists() && key == null) {
      // A file we have not even tried to open, because the key was not available. Same rule.
      _unreadable.add(conversationId);
    }

    if (bodies.isNotEmpty) _unreadable.remove(conversationId);
    _contents[conversationId] = bodies;
    final newest = bodies.values.isNotEmpty ? bodies.values.last : null;
    if (newest != null) {
      _previews.putIfAbsent(conversationId, () => _previewOf(newest));
    }
    return bodies;
  }

  /// Records a message's content on first sight, together with the sender MLS authenticated. Also
  /// becomes the conversation's preview.
  ///
  /// [attribution] is required and has no default, on purpose: every call site has an answer (the
  /// decrypt's, or "we wrote it"), and a default would let a new one quietly store a message with
  /// no author at all — which is indistinguishable, later, from a legacy entry.
  Future<void> cacheContent(
    String conversationId,
    String messageId,
    ChatContent content,
    Attribution attribution,
  ) async {
    // The UNPADDED form. Padding exists to hide a message's length from anything
    // watching the wire; nothing here reaches the wire, and padding the cache would
    // cost up to a bucket per message in secure storage for no privacy at all.
    final serialised = encodeCacheEntry(content, attribution);

    final contents = await load(conversationId);
    if (contents[messageId] == serialised) return;

    contents[messageId] = serialised;
    _previews[conversationId] = _previewOf(serialised);
    _previewSenders[conversationId] = attribution.isLegacy
        ? ''
        : attribution.userId;
    _previewIds[conversationId] = messageId;
    await _flush(conversationId, contents);
  }

  /// The stored body EXACTLY as it is serialised, or null if this device does not hold it.
  ///
  /// Raw on purpose. This is what the backup tail seals and what a restore replays, and both sides
  /// have to agree byte for byte with what [exportAllContents] produces — round-tripping through
  /// parse and re-serialise here could only lose whatever a future content version carried.
  String? raw(String conversationId, String messageId) =>
      _contents[conversationId]?[messageId];

  /// Loads the stored bodies for [conversationIds] that are not in memory yet, so the conversation
  /// list has previews to show.
  ///
  /// The list holds only ciphertext and cannot decrypt anything, so a preview can come from exactly
  /// one place: this cache. It is populated by [load], and nothing called [load] until a chat was
  /// opened — so on a fresh launch every row said "Encrypted message" and then quietly corrected
  /// itself once the chat had been visited. The bodies were on disk the whole time; nobody had
  /// asked for them.
  ///
  /// Reads one sealed file per conversation, once per launch. Not wasted work: the feed reuses the
  /// same in-memory map the moment the chat is opened.
  Future<void> warmPreviews(Iterable<String> conversationIds) async {
    for (final id in conversationIds) {
      if (_contents.containsKey(id)) continue;
      try {
        await load(id);
      } on Object {
        // One unreadable conversation must not stop the rest of the list from showing previews.
        // load() has already marked it, so nothing will be written over it either.
      }
    }
  }

  /// A message's content, if this device ever managed to read it.
  ChatContent? content(String conversationId, String messageId) =>
      entry(conversationId, messageId)?.content;

  /// A message's content AND how its author was established.
  ///
  /// The attribution comes back with the body deliberately. Reading a cached message and then
  /// asking the envelope who wrote it is exactly the hole this closes: the envelope is the server's
  /// word, and the server is the untrusted Delivery Service. See attribution.dart.
  CachedEntry? entry(String conversationId, String messageId) {
    final serialised = _contents[conversationId]?[messageId];
    if (serialised == null) return null;
    return decodeCacheEntry(serialised);
  }

  /// What a conversation-list row shows: the caption, or a note that it was a photo.
  String _previewOf(String serialised) {
    final content = parseContent(Uint8List.fromList(utf8.encode(serialised)));
    if (content.body.isNotEmpty) return content.body;
    // A photo with no caption still has to say something. An empty row reads as a bug.
    return content.hasPhotos ? '__photo__' : '';
  }

  /// The newest body seen for a conversation — the list preview.
  String? preview(String conversationId) => _previews[conversationId];

  /// The AUTHENTICATED sender of the message the preview came from, or '' when unknown.
  ///
  /// The conversation list has the same attribution problem the chat does — "is the newest message
  /// mine?" decides whether the row counts as unread — and it cannot decrypt anything to find out.
  /// It can only read what the open conversation wrote here.
  String previewSender(String conversationId) =>
      _previewSenders[conversationId] ?? '';

  /// The message the preview belongs to, so a stale preview cannot be read as an answer about a
  /// newer message.
  String previewMessageId(String conversationId) =>
      _previewIds[conversationId] ?? '';

  Future<void> _flush(
    String conversationId,
    Map<String, String> bodies, {
    bool authoritative = false,
  }) async {
    // Refuse to write over what we could not read. This is the whole of the protection against
    // turning one failed open into a lost conversation.
    //
    // Unless the caller IS the recovery. A restore has just destroyed the data key on purpose, so
    // every file here is sealed under a key that no longer exists — unopenable by anyone, forever.
    // Refusing to overwrite them protects nothing and vetoes the transcript that is the only
    // remaining copy of the history, which is how a good backup restored one conversation out of
    // twenty-three.
    if (!authoritative && _unreadable.contains(conversationId)) {
      throw ChatCacheWriteException(
        conversationId,
        'the stored bodies could not be opened; writing would replace them',
      );
    }
    final key = await _dataKey();
    if (key == null) {
      // There is no key to seal with, and the caller is holding a plaintext that exists nowhere
      // else. This used to return quietly, which threw the body away and reported success.
      throw ChatCacheWriteException(
        conversationId,
        'no data key available to seal the cache with',
      );
    }

    final sealed = await _seal(
      domain: _domain,
      key: key,
      plaintext: Uint8List.fromList(utf8.encode(jsonEncode(bodies))),
    );
    final file = await _file(conversationId);
    final temp = File('${file.path}.tmp');
    await temp.writeAsBytes(sealed, flush: true);
    await temp.rename(file.path);
  }

  /// Every conversation's bodies, raw (still-serialised) — the transcript half of the key backup,
  /// and what a history offer seals for a newly-joined device.
  ///
  /// Raw on purpose: this is a copy of the cache, not a reading of it. Round-tripping each entry
  /// through parse/serialise here could only lose information a future content version carried.
  Future<Map<String, Map<String, String>>> exportAllContents() async {
    final out = <String, Map<String, String>>{};
    final dir = await _dir();
    if (!await dir.exists()) return out;
    await for (final entry in dir.list()) {
      if (entry is! File || !entry.path.endsWith('.json')) continue;
      final name = entry.uri.pathSegments.last;
      final conversationId = name.substring(0, name.length - '.json'.length);
      final bodies = await load(conversationId);
      if (bodies.isNotEmpty) {
        out[conversationId] = Map<String, String>.from(bodies);
      }
    }
    return out;
  }

  /// Imports transcripts from a backup or a history offer — a device adopting bodies it holds none
  /// of. Merged UNDER what this device already has: anything decrypted here was read more recently
  /// than the snapshot was taken, so on a collision the local copy wins and is never overwritten.
  ///
  /// [offerer] is the device that handed these over, and it is what tells the two cases apart. A
  /// HISTORY HANDOFF comes from another device of this account, so every entry is marked as
  /// relayed: that device signed the transfer, but the author inside each message is still its
  /// claim, not something this device authenticated. A KEY BACKUP passes no offerer,
  /// because it is this account's own earlier transcript — the attributions in it were made by a
  /// device of ours, from decrypts it performed itself, and re-labelling them as relayed would be
  /// false.
  /// [authoritative] marks an import that outranks whatever is on disk: a restore from a recovery
  /// backup, which runs immediately after the data key was destroyed and so cannot open a single
  /// stored file. Ordinary imports leave it false and keep the protective refusal.
  ///
  /// A conversation that cannot be written no longer abandons the ones after it. Every entry is
  /// attempted, and the failures are raised together at the end — one bad conversation used to cost
  /// the whole transcript, and which conversations survived depended on map iteration order.
  Future<void> importContents(
    Map<String, Map<String, String>> all, {
    String offerer = '',
    bool authoritative = false,
  }) async {
    final failures = <String, Object>{};
    for (final entry in all.entries) {
      final conversationId = entry.key;
      try {
        await _importOne(entry, offerer: offerer, authoritative: authoritative);
      } on Object catch (e) {
        failures[conversationId] = e;
      }
    }
    if (failures.isNotEmpty) {
      throw ChatCacheWriteException(
        failures.keys.join(', '),
        'could not store ${failures.length} of ${all.length} conversations: '
        '${failures.values.first}',
      );
    }
  }

  Future<void> _importOne(
    MapEntry<String, Map<String, String>> entry, {
    required String offerer,
    required bool authoritative,
  }) async {
    {
      final conversationId = entry.key;
      final contents = await load(conversationId);
      var changed = false;
      entry.value.forEach((id, serialised) {
        if (!contents.containsKey(id)) {
          // Stamped with WHO handed it over. An offerer signed the transfer with its leaf key, so
          // the claim is attributable to our other device — but it is that device's word about who
          // wrote each message, not something this device authenticated, and the two must never
          // become indistinguishable in the cache.
          contents[id] = offerer.isEmpty
              ? serialised
              : markRelayed(serialised, offerer);
          changed = true;
        }
      });
      if (!changed) return;
      final newest = contents.values.isNotEmpty ? contents.values.last : null;
      if (newest != null) _previews[conversationId] = _previewOf(newest);
      await _flush(conversationId, contents, authoritative: authoritative);
      // Whatever could not be opened has now been replaced by something that can be.
      if (authoritative) _unreadable.remove(conversationId);
    }
  }

  /// Forgets a conversation's bodies — it was deleted.
  Future<void> forget(String conversationId) async {
    _contents.remove(conversationId);
    _previews.remove(conversationId);
    _previewSenders.remove(conversationId);
    _previewIds.remove(conversationId);
    final file = await _file(conversationId);
    if (await file.exists()) await file.delete();
  }

  /// Erases every decrypted body. Logout: this is the plaintext the encryption exists to protect,
  /// and leaving it behind on a shared device would defeat the whole thing.
  Future<void> wipe() async {
    _contents.clear();
    _previews.clear();
    _previewSenders.clear();
    _previewIds.clear();
    // The files these referred to are about to be deleted, so there is nothing left to protect. Left
    // behind, a conversation marked unreadable before the wipe stayed unwritable for the life of the
    // process — including to the restore that was trying to repopulate it.
    _unreadable.clear();
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
  }
}
