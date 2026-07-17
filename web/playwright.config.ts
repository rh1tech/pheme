import { defineConfig, devices } from '@playwright/test'
import { ADMIN_EMAIL, ADMIN_PASSWORD, API_PORT, API_URL, WEB_PORT, WEB_URL } from './e2e/constants'

const isCI = !!process.env.CI

// Two servers back the suite: the Go App API (in-memory drivers, zero external
// infra, with a seeded admin) and the Vite dev server pointed at it. Playwright
// starts both, waits for them to be ready, and tears them down afterwards.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  forbidOnly: isCI,
  retries: isCI ? 1 : 0,
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
    { name: 'mobile-safari', use: { ...devices['iPhone 13'] } },
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
        PHEME_JWT_SECRET: 'e2e-test-secret',
        PHEME_MAIL_DRIVER: 'log',
        PHEME_SEED_ADMIN_EMAIL: ADMIN_EMAIL,
        PHEME_SEED_ADMIN_PASSWORD: ADMIN_PASSWORD,
        // Calling on, with NO ICE servers: the two browsers are on the same machine and reach
        // each other on their own host candidates. Naming a STUN server that is not really
        // there costs ten seconds per call while the ICE agent waits for it to time out, and
        // naming a real public one would make the suite depend on the internet.
        PHEME_TURN_URLS: 'direct',
      },
    },
    {
      // The E2E runs against a PRODUCTION BUILD served statically, not the Vite dev server.
      // The dev server transpiles per request and holds every module live; under this suite —
      // dozens of real-WASM crypto contexts across many tests — it was choking and aborting
      // navigations (net::ERR_ABORTED), which read as flaky test failures. A built bundle served
      // by `vite preview` is static and far lighter, and it exercises the artifact we actually
      // ship. VITE_API_BASE is baked in at build time (see src/lib/api.ts).
      command: `npm run build && npm run preview -- --port ${WEB_PORT} --strictPort`,
      url: WEB_URL,
      reuseExistingServer: !isCI,
      timeout: 180_000,
      env: { VITE_API_BASE: API_URL },
    },
  ],
})
