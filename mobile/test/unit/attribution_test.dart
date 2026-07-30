// Who a cached message is attributed to, and how far that attribution can be trusted.
//
// This is the layer between "MLS authenticated a credential" and "a bubble renders a name", and it
// is where the bug that started all of this actually bit: the app had the authenticated sender
// nowhere, so every surface — the bubble, the quote, the conversation row, the notification —
// answered "who wrote this?" from the envelope, which the untrusted server writes.
//
// Pure by construction (strings in, strings out), so every rule below is exercised without the Rust
// library, a session or a group. The web has the same suite over the same rules in
// web/src/lib/attribution.test.ts — the two clients share this format inside a history handoff.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/attribution.dart';
import 'package:pheme_mobile/src/crypto/chat_content.dart';

const alice = 'mimi://test.example/d/alice/dev-a';
const bob = 'mimi://test.example/d/bob/dev-b';

void main() {
  group('Attribution.authenticated', () {
    test('reduces a credential to the bare user the roster is keyed by', () {
      final a = Attribution.authenticated(alice);
      expect(a.kind, AttributionKind.mls);
      expect(a.userId, 'alice');
      expect(a.identity, alice);
    });

    test('is legacy for anything that is not a resolvable credential', () {
      // A pre-device-id leaf (`user:device`), or an empty sender. Neither names a user we could
      // compare against a membership list, and inventing one is how a message gets rendered under
      // somebody else's name.
      expect(Attribution.authenticated('alice:dev-a').isLegacy, isTrue);
      expect(Attribution.authenticated('').isLegacy, isTrue);
    });
  });

  group('the cache entry', () {
    const content = ChatContent(body: 'hello', replyTo: 'm-0');

    test('carries the sender alongside the body', () {
      final back = decodeCacheEntry(
        encodeCacheEntry(content, Attribution.authenticated(alice)),
      );
      expect(back.content.body, 'hello');
      expect(back.content.replyTo, 'm-0');
      expect(back.attribution, Attribution.authenticated(alice));
    });

    // Every message anybody decrypted before this existed is one of these, and the MLS key that
    // could have re-derived the sender is long gone. Dropping them would destroy the only copy of
    // that plaintext there will ever be.
    test('reads an entry from before senders were stored, as legacy', () {
      final back = decodeCacheEntry('{"body":"from an older build"}');
      expect(back.content.body, 'from an older build');
      expect(back.attribution.isLegacy, isTrue);
    });

    test('does not treat an unparseable sender as an attribution', () {
      final back = decodeCacheEntry('{"body":"hi","_s":"alice:dev-a"}');
      expect(back.attribution.isLegacy, isTrue);
    });

    // The extra fields ride on the same object, so a build that has never heard of `_s` still finds
    // body, replyTo and photos exactly where it expects them — which is what keeps a transcript
    // readable across clients and across versions.
    test('keeps the content fields where an older reader looks for them', () {
      final raw = encodeCacheEntry(content, Attribution.authenticated(alice));
      expect(raw, contains('"body":"hello"'));
      expect(raw, contains('"replyTo":"m-0"'));
      expect(raw, contains('"_s":"$alice"'));
      // And the plain content parser still reads it, which is the compatibility guarantee itself.
      expect(parseContent(Uint8List.fromList(utf8.encode(raw))).body, 'hello');
    });
  });

  group('imported history', () {
    // A co-member signs the whole transfer with its leaf key, so the transcript is attributable to
    // a real member of the group. The per-message author inside it is still that member's WORD —
    // this device did not authenticate it — and the two must never become indistinguishable.
    test('marks an imported entry with who handed it over', () {
      final original = encodeCacheEntry(
        const ChatContent(body: 'said before I joined'),
        Attribution.authenticated(alice),
      );
      final imported = decodeCacheEntry(markRelayed(original, bob));
      expect(imported.attribution.kind, AttributionKind.relayed);
      expect(imported.attribution.userId, 'alice');
      expect(imported.attribution.relayedBy, bob);
      expect(imported.content.body, 'said before I joined');
    });

    test('does not invent an author for an imported entry that names none', () {
      final imported = decodeCacheEntry(
        markRelayed('{"body":"no sender in here"}', bob),
      );
      expect(imported.attribution.isLegacy, isTrue);
    });

    test('never presents relayed authorship as verified', () {
      final relayed = Attribution.relayed(alice, bob);
      expect(resolveAuthor(relayed, 'alice').verified, isFalse);
    });
  });

  group('resolveAuthor', () {
    test('renders an MLS-authenticated message under the signer', () {
      final view = resolveAuthor(Attribution.authenticated(alice), 'alice');
      expect(view.userId, 'alice');
      expect(view.verified, isTrue);
      expect(view.tampered, isFalse);
    });

    // THE ATTACK, caught. The server relayed Bob's ciphertext with Alice's id on the envelope — or
    // Alice's ciphertext under Bob's. Either way the two disagree, and picking one of them silently
    // is exactly what the authenticated sender exists to prevent.
    test('reports a mismatch between the signature and the envelope', () {
      final view = resolveAuthor(Attribution.authenticated(alice), 'bob');
      expect(view.userId, 'alice');
      expect(view.verified, isFalse);
      expect(view.tampered, isTrue);
    });

    test('falls back to the envelope for a legacy entry, unverified', () {
      final view = resolveAuthor(Attribution.legacy, 'alice');
      expect(view.userId, 'alice');
      expect(view.verified, isFalse, reason: 'never labelled as verified');
      expect(view.tampered, isFalse);
    });

    test('reports no mismatch when there is no envelope claim to compare', () {
      expect(
        resolveAuthor(Attribution.authenticated(alice), '').tampered,
        isFalse,
      );
    });
  });

  group('isOwnMessage', () {
    test('decides from the signature, not the envelope', () {
      // The server claiming our own id on somebody else's message must not put it on our side of
      // the feed — which is where a reader assumes they wrote it themselves.
      expect(
        isOwnMessage(Attribution.authenticated(bob), 'alice', 'alice'),
        isFalse,
      );
      expect(
        isOwnMessage(Attribution.authenticated(alice), 'bob', 'alice'),
        isTrue,
      );
    });

    test('uses the envelope only where there is no plaintext', () {
      // A message this device cannot read at all has no attribution. The envelope is genuinely all
      // that exists for it, and the bubble still has to land on one side or the other.
      expect(isOwnMessage(null, 'alice', 'alice'), isTrue);
      expect(isOwnMessage(Attribution.legacy, 'alice', 'alice'), isTrue);
      expect(isOwnMessage(Attribution.legacy, 'bob', 'alice'), isFalse);
    });

    test('is nobody\'s message before we know who we are', () {
      expect(
        isOwnMessage(Attribution.authenticated(alice), 'alice', ''),
        isFalse,
      );
    });
  });
}
