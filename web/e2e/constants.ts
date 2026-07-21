// Shared constants for the E2E suite and Playwright config. The admin credentials
// match the PHEME_SEED_ADMIN_* env passed to the API web server in
// playwright.config.ts, so the seeded admin can always log in.

// Dedicated ports for the E2E stack, chosen to avoid colliding with a running
// `make dev` stack (App API :8080, Vite :5173).
//
// PORTS ARE PER-SHARD. The two CI runners are two runner instances on ONE host, so sharded E2E
// jobs run side by side on the same machine and share its network. With fixed ports, shard 2's
// App API would try to bind the port shard 1 already holds — "address already in use" — and,
// worse, a browser could reach the OTHER shard's server and see the wrong state. PW_PORT_OFFSET
// (set per shard in ci.yml) moves each shard's whole stack to its own pair of ports. Unset (local
// runs, a single unsharded run) it is 0, giving the original ports.
const _offset = Number(process.env.PW_PORT_OFFSET ?? '0') * 20
export const WEB_PORT = 4317 + _offset
export const API_PORT = 8099 + _offset
export const WEB_URL = `http://localhost:${WEB_PORT}`
export const API_URL = `http://localhost:${API_PORT}`

export const ADMIN_EMAIL = 'admin@pheme.test'
export const ADMIN_PASSWORD = 'Admin12345'

// Two FEDERATED App API hosts for the cross-host E2EE spec — host A (a.test) and
// host B (b.test), each a full in-memory app with a seeded admin, sharing the
// signed nodelist fixture in e2e/fixtures and reaching each other over loopback
// via PHEME_PEER_URLS. Their ports sit above the single-host stack's, offset-aware.
export const FED_A_PORT = 8110 + _offset
export const FED_B_PORT = 8112 + _offset
export const FED_A_URL = `http://localhost:${FED_A_PORT}`
export const FED_B_URL = `http://localhost:${FED_B_PORT}`
export const FED_A_DOMAIN = 'a.test'
export const FED_B_DOMAIN = 'b.test'
// Test host keys (base64url seeds) matching the committed nodelist fixture. Test
// material, not secret — the same fixed-seed pattern the Go federation tests use.
export const FED_A_HOST_KEY = '2hvdqi3oQrIXu9KIb9i7oqjKfTnuQfN5mObmbQWsMnQ'
export const FED_B_HOST_KEY = 'o7-R1YMGuaLaCqDMXCU4rovUxphwofE0ANlqq9za5Ic'
export const FED_COORD_KEY = 'YTSBtW9CZ-Xw7SOE3UVFJJtHmKxb6hn0whiUpDevzlI'
