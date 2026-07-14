import { defineConfig } from 'vitest/config'

// Unit tests only. `e2e/` is Playwright's — its specs import `@playwright/test`, which is a different
// runner with a different `test` symbol, and letting vitest collect them makes every one of them fail
// for reasons that have nothing to do with the code.
export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
  },
})
