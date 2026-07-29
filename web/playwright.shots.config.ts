import { defineConfig, devices } from '@playwright/test'
import base from './playwright.config'

/**
 * The screenshot pass, kept out of the E2E suite on purpose.
 *
 * These are not tests. They seed a instance full of plausible-looking data and
 * photograph it, they expect accounts that scripts/seed create, and one of them
 * shells out to a local Mongo container to backdate timestamps. Run inside the
 * ordinary suite they would fail in CI for all three reasons, and they would
 * write into ../screenshots on every run.
 *
 * So they live in their own directory with their own config:
 *
 *   npx playwright test --config=playwright.shots.config.ts
 *
 * `npx playwright test` — what CI runs — does not see them at all, because the
 * default config's testDir is ./e2e.
 */
export default defineConfig({
  ...base,
  testDir: './e2e-shots',
  // One at a time: several of these drive a dozen browser contexts each, and
  // they seed shared server state that parallel runs would tread on.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
