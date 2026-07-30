# pheme-mls — the E2EE client core

A stateful [MLS](https://www.rfc-editor.org/rfc/rfc9420) (RFC 9420) client, on
[OpenMLS](https://github.com/openmls/openmls), that the web app drives (via WASM)
to run end-to-end-encrypted conversations.

The **server never appears here**. In MLS it is an untrusted Delivery Service: it
stores public KeyPackages and relays the opaque bytes these methods produce, in
order. All key material and group ratchet state stay on the device.

## The one rule: a leaf is a device, not a person

Two devices of the same user are two independent clients holding different private
keys. **Each must occupy its own leaf**, or it cannot decrypt a single message sent
to the group. So the credential identity is `userId:deviceId`, a group is built from
one KeyPackage per *device* of each member, and removing somebody removes *every*
leaf they hold — not whichever one the tree happened to find first.

Getting this wrong is not subtle in its effects and is completely silent in its
symptoms: the user simply sees a conversation of blanks on one of their devices, and
every unit test stays green.

## Commits are staged, not applied

`stage_add` / `stage_remove_users` / `stage_remove_devices` build a Commit but do
**not** merge it. The caller sends it to the server, which accepts it only if the
group is still at the epoch it was built against, and then calls `commit_accepted`
(merge) or `commit_rejected` (discard).

That order is the whole point. Two members can Commit against the same epoch, only
one can win, and a client that has already advanced its own ratchet on a Commit the
group never accepted is forked off the conversation — permanently, and silently.

## API (`Client`)

- `new(user_id, device_id)` / `import_state(bytes)` — create or restore a client.
- `key_package()` — a single-use public KeyPackage to publish.
  `last_resort_key_package()` — the reusable one that keeps a user reachable when a
  stranger drains their stock.
- `create_group(id)`, `join_from_welcome(welcome)`, `apply_commit(id, commit)`.
- `stage_add(id, kps) -> {welcome, commit}` — add devices as leaves.
  `stage_remove_users(id, users)` — throw people out (all their devices).
  `stage_remove_devices(id, identities)` — prune a ghost leaf, sparing that person's
  live devices.
  `commit_accepted(id)` / `commit_rejected(id)` — the server's verdict.
- `epoch(id)`, `member_identities(id)` — what a caller diffs to find missing devices.
- `encrypt(id, plaintext)`, `decrypt(id, ciphertext) -> Option<Decrypted>`. A decrypt
  returns the plaintext **together with the sender MLS authenticated** — the credential
  of the leaf whose signature `process_message` verified against the group's own ratchet
  tree — and the epoch the message was framed in. An application message that does not
  come from a member leaf is refused rather than returned unattributed. This is the only
  trustworthy answer to "who wrote this": the `senderId` on the message envelope is
  written by the server, which relays these bytes and can put any name on them.
- `sign_history_request` / `verify_history_request` / `sign_history_offer` /
  `verify_history_offer` — the sender-authenticated device-to-device history handoff.
  The exporter secret that seals a transferred transcript proves only that the sender is
  *a* member, because every member derives it; these sign a canonical, domain-separated
  transcript (see `src/history.rs`) with the member's own leaf key and verify it against
  the leaf key the ratchet tree holds for the claimed identity. The transcript is built
  in the crate, so the browser and the phone canonicalise identically by construction.
  Because any group member can sign invented history as themselves, clients additionally
  accept a provider only when it is another device of the requester's domain-qualified
  account.
- `export_secret(id, label, context, len)` — RFC 9420's exporter. Every member of the
  group derives the same bytes for the same (label, context); nobody else can. Used to
  key voice-call signalling, so the server cannot read the SDP and therefore cannot
  swap the DTLS fingerprint inside it and put itself in the middle of a call. A pure
  read: it mutates neither the group nor the storage, which is why it suits signalling
  and an ordinary application message does not (those are one-shot to decrypt, and they
  churn the ratchet and the key store on every send). **Bound to the current epoch** —
  a caller must pin the epoch it derived at.
- `export_state()` — the whole client state for persistence (IndexedDB on web).

`src/wasm.rs` wraps this as `MlsClient` for JavaScript.

## Pinned by tests

`cargo test`, plus the real-browser Playwright suite over the `wasm-pack` bundle
(`web/e2e/chat-multidevice.spec.ts`):

- **every device of a member can read** — including messages sent by that member's
  *other* device (a sender cannot decrypt their own, but a sibling device can);
- a new device is **added** to the group, never rebuilt around — the history other
  members hold survives;
- removing a member cuts off **all** their devices;
- a rejected Commit leaves the loser able to catch up and retry, unforked;
- a message from a past epoch still decrypts after a membership change
  (`MAX_PAST_EPOCHS`; OpenMLS defaults to 0, which silently destroys history);
- a Welcome burns the ordinary KeyPackage it names even when forged — hence the
  last-resort package, which is immune;
- an outsider cannot decrypt, and the stored ciphertext contains no plaintext.

## Notes surfaced along the way

- OpenMLS needs its `js` feature to compile for `wasm32` (browser randomness +
  clock); it refuses otherwise.
- The sub-crates must match the versions `openmls` itself pins (0.5.x), or the
  `Signer` trait resolves to two crate versions and won't unify.
- `MemoryStorage::serialize` is behind a test-utils feature, so state is persisted
  by serialising its public `values` map directly.
- `use_ratchet_tree_extension` must be set in the **join** config too, not only the
  create config — otherwise a member who joined from a Welcome produces Welcomes with
  no ratchet tree, and nobody can join from *them* (`MissingRatchetTree`). That
  matters because adding a device is no longer the creator's privilege.
- MLS forbids committing your own removal (`CannotRemoveSelf`), so leaving a group is
  not a Commit: the member drops their membership and destroys their local state, and
  the members who remain prune the leaves left behind when they next reconcile.

## Build

    cargo test                                              # native
    wasm-pack build --release --target web --out-dir pkg    # browser bundle

The bundle is **vendored** into `web/src/crypto/pkg/` and committed — the web build
does not run `wasm-pack`. After changing this crate, rebuild and copy:

    cp pkg/pheme_mls.js pkg/pheme_mls.d.ts pkg/pheme_mls_bg.wasm \
       pkg/pheme_mls_bg.wasm.d.ts ../../web/src/crypto/pkg/

There is a SECOND vendored copy, and forgetting it is the failure this note exists to
prevent. The service worker cannot use an ES module, so it loads a `no-modules` build of
the same crate from `web/public/mls/`:

    wasm-pack build --release --target no-modules --out-dir pkg-nomodules
    cp pkg-nomodules/pheme_mls.js ../../web/public/mls/pheme_mls_nomodules.js

Only the GLUE is vendored there — the worker loads `/mls/pheme_mls_bg.wasm`, which vite
serves from `web/src/crypto/pkg/`. So the two must be rebuilt TOGETHER: a glue from one
build talking to a binary from another is an ABI mismatch, and it fails at whichever
export happens to have shifted rather than anywhere obvious. That is exactly what
happened — the glue was committed once with the previews feature and never regenerated,
so when `MlsClient::new` gained its `domain` argument the glue still passed two strings
to a function expecting three. Production was unharmed (the worker only ever calls
`MlsPreviewClient`, whose ABI had not moved), but the notification-preview E2E failed on
every run for weeks and was read as flake.
