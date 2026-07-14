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
- `encrypt(id, plaintext)`, `decrypt(id, ciphertext)`.
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
