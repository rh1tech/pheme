import { defineConfig } from 'vitest/config'

// Unit tests only. `e2e/` is Playwright's — its specs import `@playwright/test`, which is a different
// runner with a different `test` symbol, and letting vitest collect them makes every one of them fail
// for reasons that have nothing to do with the code.
export default defineConfig({
  test: {
    // `test/` is for specs that need Node itself — reading the WASM binary off disk, for one.
    // They live outside src/ so that app code keeps its narrow `types: ["vite/client"]` and
    // cannot quietly start depending on Node globals that do not exist in a browser.
    include: ['src/**/*.test.ts', 'test/**/*.test.ts'],
  },
})
