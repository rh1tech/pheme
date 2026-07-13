# pheme-mls — the E2EE client core

A stateful [MLS](https://www.rfc-editor.org/rfc/rfc9420) (RFC 9420) client, on
[OpenMLS](https://github.com/openmls/openmls), that the web (WASM) and mobile
(Flutter, via flutter_rust_bridge) apps drive to run end-to-end-encrypted
conversations.

The **server never appears here**. In MLS it is an untrusted Delivery Service: it
stores public KeyPackages and relays the opaque bytes these methods produce, in
order. All key material and group ratchet state stay on the device.

## API (`Client`)

- `new(identity)` / `import_state(bytes)` — create or restore a client.
- `key_package()` — a single-use public KeyPackage to publish to the server.
- `create_group(id)`, `add_member(id, kp) -> {welcome, commit}`,
  `join_from_welcome(welcome)`, `apply_commit(id, commit)`.
- `encrypt(id, plaintext)`, `decrypt(id, ciphertext)`.
- `export_state()` — the whole client state (identity + every group) for
  persistence (IndexedDB on web, secure storage on mobile).

`src/wasm.rs` wraps this as `MlsClient` for JavaScript.

## Proven

Tested native (`cargo test`) and in a real browser (Playwright over the
`wasm-pack` bundle):

- two-party encrypt → relay → decrypt round-trip;
- **state survives export → drop → import** — the restored client still decrypts
  and sends (the property IndexedDB persistence relies on);
- an outsider not in the group cannot decrypt;
- the ciphertext the server would store contains **no plaintext**.

## Notes surfaced along the way

- OpenMLS needs its `js` feature to compile for `wasm32` (browser randomness +
  clock); it refuses otherwise.
- The sub-crates must match the versions `openmls` itself pins (0.5.x), or the
  `Signer` trait resolves to two crate versions and won't unify.
- `MemoryStorage::serialize` is behind a test-utils feature, so state is persisted
  by serialising its public `values` map directly.

## Build

    cargo test --release                                    # native
    cargo build --release --target wasm32-unknown-unknown   # wasm check
    wasm-pack build --release --target web --out-dir pkg    # browser bundle
