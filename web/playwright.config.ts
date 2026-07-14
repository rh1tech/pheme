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
        // Calling on, with a STUN server only. The two browsers in a test are on the same
        // host, so they reach each other directly — no TURN relay is needed, and none is
        // configured, which also means the test would notice if the code ever started
        // depending on one.
        PHEME_TURN_URLS: 'stun:stun.l.google.com:19302',
        PHEME_TURN_SECRET: 'e2e-turn-secret',
      },
    },
    {
      command: `npm run dev -- --port ${WEB_PORT} --strictPort`,
      url: WEB_URL,
      reuseExistingServer: !isCI,
      timeout: 120_000,
      env: { VITE_API_BASE: API_URL },
    },
  ],
})
