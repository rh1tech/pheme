import { defineConfig, devices } from '@playwright/test'
import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  API_PORT,
  API_URL,
  FED_A_DOMAIN,
  FED_A_HOST_KEY,
  FED_A_PORT,
  FED_A_URL,
  FED_B_DOMAIN,
  FED_B_HOST_KEY,
  FED_B_PORT,
  FED_B_URL,
  FED_COORD_KEY,
  WEB_PORT,
  WEB_URL,
} from './e2e/constants'

const isCI = !!process.env.CI

// Two servers back the suite: the Go App API (in-memory drivers, zero external
// infra, with a seeded admin) and the Vite dev server pointed at it. Playwright
// starts both, waits for them to be ready, and tears them down afterwards.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: isCI,
  // Two retries in CI. The crypto suite drives real WASM + WebRTC contexts, and under the
  // dev server the odd navigation still aborts (net::ERR_ABORTED) purely from load — a flake
  // in the harness, not the product. A retry clears it; the assertions themselves are
  // deterministic. Locally, no retries — a failure there should be seen, not papered over.
  retries: isCI ? 2 : 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: isCI ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: WEB_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // A real microphone and a real WebRTC stack, without a prompt and without hardware.
        // Chromium synthesises an audio device, so getUserMedia resolves and the peer
        // connection carries genuine RTP — which is what the call test actually asserts on.
        launchOptions: {
          args: [
            '--use-fake-device-for-media-stream',
            '--use-fake-ui-for-media-stream',
            '--autoplay-policy=no-user-gesture-required',
            // Chrome hides local IPs behind mDNS (.local) ICE candidates, which the two
            // browsers then have to resolve over multicast. That is right for real users and
            // useless here: both ends are the same machine, and mDNS resolution in a sandbox
            // is slow and unreliable enough to make the call fail intermittently. Real host
            // candidates make the connection deterministic.
            '--disable-features=WebRtcHideLocalIpsWithMdns',
            // Gather candidates on the default route only.
            //
            // A developer machine can easily have a dozen interfaces — VM host-only networks,
            // virtual bridges, IPv6 ULAs — and Chrome offers a host candidate on every one of
            // them. ICE then has to try every pair, most of which are dead ends that do not
            // loop back, and the call connects or fails depending on the order it happens to
            // get to a working one. That is not a property of the call; it is a property of the
            // laptop. One interface, one pair, deterministic.
            '--allow-loopback-in-peer-connection',
          ],
        },
        permissions: ['microphone'],
      },
    },
    {
      name: 'mobile-safari',
      use: {
        ...devices['iPhone 13'],
        // No service worker under WebKit.
        //
        // The app registers a worker on every page load and immediately calls update() on it. That
        // is right in production — it is what stops installs running an old worker — and it is what
        // made this project intermittently red. Under WebKit each registration races the navigation
        // that triggered it, and roughly three tests per run would sit until the 30-second timeout,
        // never the same three. Measured across full runs: 19 passed / 3 failed twice in a row with
        // workers on, 21/1 and then 22/0 with them blocked.
        //
        // Nothing is given up by blocking them here. Playwright can only observe service workers on
        // Chromium — context.serviceWorkers() is empty everywhere else and waitForEvent('serviceworker')
        // never fires — so the one test that asserts on worker behaviour is already chromium-only
        // and says so. Registering a worker this project cannot see bought no coverage and cost
        // stability.
        //
        // Safari's real worker behaviour still matters, and still belongs to on-device testing.
        serviceWorkers: 'block',
      },
    },
  ],
  webServer: [
    {
      command: 'go run ./cmd/app',
      cwd: '../api',
      url: `${API_URL}/healthz`,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      env: {
        PHEME_APP_ADDR: `:${API_PORT}`,
        // The API only answers CORS for origins it is told about, and the suite's
        // web server is on its own port (per shard, see e2e/constants.ts) — not the
        // :5173 the API defaults to for `make dev`. Without this every browser
        // request in the suite fails as a CORS error.
        PHEME_CORS_ORIGINS: WEB_URL,
        PHEME_JWT_SECRET: 'e2e-test-secret',
        PHEME_MAIL_DRIVER: 'log',
        // The suite signs the seeded admin in dozens of times, which a budget
        // sized for a human would throttle. Production keeps the tight default.
        PHEME_AUTH_RATE_BURST: '10000',
        PHEME_SEED_ADMIN_EMAIL: ADMIN_EMAIL,
        PHEME_SEED_ADMIN_PASSWORD: ADMIN_PASSWORD,
        // Calling on, with NO ICE servers: the two browsers are on the same machine and reach
        // each other on their own host candidates. Naming a STUN server that is not really
        // there costs ten seconds per call while the ICE agent waits for it to time out, and
        // naming a real public one would make the suite depend on the internet.
        PHEME_TURN_URLS: 'direct',
      },
    },
    // Two federated App API hosts for the cross-host E2EE spec. Each is a full
    // in-memory app that trusts the committed nodelist fixture and reaches the
    // other over loopback via PHEME_PEER_URLS. Only the cross-host spec drives
    // them (through per-context apiBase overrides); every other spec ignores them.
    {
      command: 'go run ./cmd/app',
      cwd: '../api',
      url: `${FED_A_URL}/healthz`,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      env: {
        PHEME_APP_ADDR: `:${FED_A_PORT}`,
        PHEME_CORS_ORIGINS: WEB_URL,
        PHEME_JWT_SECRET: 'e2e-fed-a-secret',
        PHEME_MAIL_DRIVER: 'log',
        // The suite signs the seeded admin in dozens of times, which a budget
        // sized for a human would throttle. Production keeps the tight default.
        PHEME_AUTH_RATE_BURST: '10000',
        PHEME_SEED_ADMIN_EMAIL: `alice@${FED_A_DOMAIN}`,
        PHEME_SEED_ADMIN_PASSWORD: ADMIN_PASSWORD,
        PHEME_TURN_URLS: 'direct',
        PHEME_HOST_DOMAIN: FED_A_DOMAIN,
        PHEME_HOST_KEY: FED_A_HOST_KEY,
        PHEME_NODELIST_COORD_KEY: FED_COORD_KEY,
        PHEME_NODELIST_PATH: '../web/e2e/fixtures/nodelist.json',
        PHEME_PEER_URLS: `${FED_B_DOMAIN}=${FED_B_URL}`,
      },
    },
    {
      command: 'go run ./cmd/app',
      cwd: '../api',
      url: `${FED_B_URL}/healthz`,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      env: {
        PHEME_APP_ADDR: `:${FED_B_PORT}`,
        PHEME_CORS_ORIGINS: WEB_URL,
        PHEME_JWT_SECRET: 'e2e-fed-b-secret',
        PHEME_MAIL_DRIVER: 'log',
        // The suite signs the seeded admin in dozens of times, which a budget
        // sized for a human would throttle. Production keeps the tight default.
        PHEME_AUTH_RATE_BURST: '10000',
        PHEME_SEED_ADMIN_EMAIL: `bob@${FED_B_DOMAIN}`,
        PHEME_SEED_ADMIN_PASSWORD: ADMIN_PASSWORD,
        PHEME_TURN_URLS: 'direct',
        PHEME_HOST_DOMAIN: FED_B_DOMAIN,
        PHEME_HOST_KEY: FED_B_HOST_KEY,
        PHEME_NODELIST_COORD_KEY: FED_COORD_KEY,
        PHEME_NODELIST_PATH: '../web/e2e/fixtures/nodelist.json',
        PHEME_PEER_URLS: `${FED_A_DOMAIN}=${FED_A_URL}`,
      },
    },
    {
      // The Vite DEV server, not a production `vite preview`.
      //
      // A production build was tried, to escape the net::ERR_ABORTED the dev server threw when
      // the WHOLE suite — dozens of real-WASM crypto contexts — ran against it in one process.
      // But the production build silently broke the CALL tests: peer-to-peer audio never flowed
      // (inboundAudioPackets stuck at -1) though every call test passes here on the dev server.
      // A green crypto run that quietly stops testing calls is worse than the abort it fixed.
      //
      // The real cause of the abort was accumulation, not the dev server itself: the suite is now
      // sharded across the runners (see ci.yml), so each process runs half of it, and the dev
      // server carries that comfortably — the WASM-heavy Phase 2/3 specs and the call tests both
      // pass. VITE_API_BASE reaches the client through Vite's env at dev time (see src/lib/api.ts).
      command: `npm run dev -- --port ${WEB_PORT} --strictPort`,
      url: WEB_URL,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      env: { VITE_API_BASE: API_URL },
    },
  ],
})
