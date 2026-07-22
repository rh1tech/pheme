import { test as base, type BrowserContext } from '@playwright/test'

/** Contexts opened by the current test, closed when it ends however it ends. */
const opened = new Set<BrowserContext>()

/**
 * Registers a BrowserContext for automatic close at the end of the test.
 *
 * Called by signInOnNewDevice, so every "device" a crypto spec opens is covered without the
 * spec having to remember anything.
 */
export function trackContext(context: BrowserContext): void {
  opened.add(context)
}

/**
 * The suite's `test`, with one thing added: every device context a test opens is closed when
 * that test ends, whether it passed, failed, or timed out.
 *
 * WHY THIS EXISTS. The crypto specs model a device as a BrowserContext, and a test that needs
 * four devices opens four. They were closed on the last line of the test body:
 *
 *     await Promise.all([alice.context.close(), bob.context.close()])
 *
 * which runs only if everything above it succeeded. A test that failed — or, worse, timed out —
 * never reached it, and the contexts stayed open. `browser` is WORKER-scoped and the suite runs
 * with a single worker, so those contexts survived not just that test but the ENTIRE REST OF THE
 * RUN: a leaked context is a live Chromium profile still running the PWA, its WASM crypto, its
 * service worker and its open event stream.
 *
 * That turned one failure into a cascade. Every test after the leak ran with more contended CPU
 * and memory than the timeouts were tuned for, so the next marginal test timed out too, and
 * leaked in its turn. The signature was a suite that failed in a DIFFERENT place on every run
 * while each of those tests passed on its own — which reads exactly like flake, and is not: it is
 * one real failure plus a resource leak amplifying it. Locally this cost roughly a minute a run
 * and two spurious failures.
 *
 * Tests may still close their contexts explicitly, and they do; closing twice is a no-op. This is
 * the floor under them, not a replacement.
 *
 * Deliberately NOT done by wrapping `browser.newContext`: that would also capture the contexts
 * Playwright creates for its own `page` fixture, and closing those out from under the runner is
 * a different bug traded for this one. Registration is explicit, in the one helper every device
 * goes through.
 */
export const test = base.extend<{ autoCloseContexts: void }>({
  autoCloseContexts: [
    // Playwright requires the first argument to be an object destructuring pattern even when the
    // fixture uses none of them, which is the one case eslint's no-empty-pattern cannot know about.
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      opened.clear()
      await use()
      // Best effort and never fatal: a context the test already closed rejects nothing, and a
      // cleanup that could fail the run would be worse than the leak it exists to prevent.
      for (const context of opened) await context.close().catch(() => {})
      opened.clear()
    },
    { auto: true },
  ],
})

export { expect } from '@playwright/test'
