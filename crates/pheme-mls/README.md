# pheme-mls — feasibility spike (Phase 0)

Proves that OpenMLS (RFC 9420) is viable for Pheme's end-to-end-encrypted chats:

- `roundtrip()` performs a two-party MLS exchange (create group → add member via
  KeyPackage → encrypt → serialize to wire → decrypt), passing as a native test.
- The same crate compiles to `wasm32-unknown-unknown` (OpenMLS needs its `js`
  feature there) and, bundled with `wasm-pack build --target web`, runs the
  round-trip from browser JavaScript — verified with Playwright.

The server never appears here: in MLS it is an untrusted Delivery Service that
only relays the opaque bytes these functions produce. This is a spike, not
production crypto — the real client API (keygen / group / send / receive, key
storage, backup) lands in Phase 3.

## Run
    cargo test --release                              # native round-trip
    wasm-pack build --release --target web --out-dir pkg   # browser bundle
