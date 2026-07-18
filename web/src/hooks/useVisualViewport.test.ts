// The recovery path for a viewport update iOS never sent.
//
// This is the bug these tests exist for: the app rendered into the top two-thirds of an iPhone
// screen with a band of page background beneath it, because the shell was still sized for a
// keyboard that had already gone. iOS drops the final `visualViewport` resize as the keyboard
// animates away — reliably, in an installed PWA — and nothing else was listening.
//
// Rendering the hook would not catch it: the hook is correct, and the event simply never arrives.
// So the subscription is tested directly, with a viewport that goes quiet at exactly the wrong
// moment.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { subscribeToViewport } from './useVisualViewport'

/** A minimal event target standing in for window/document/visualViewport. */
function fakeTarget() {
  const handlers = new Map<string, Set<() => void>>()
  return {
    addEventListener: (type: string, fn: () => void) => {
      if (!handlers.has(type)) handlers.set(type, new Set())
      handlers.get(type)!.add(fn)
    },
    removeEventListener: (type: string, fn: () => void) => {
      handlers.get(type)?.delete(fn)
    },
    emit: (type: string) => {
      for (const fn of handlers.get(type) ?? []) fn()
    },
    listenerCount: () =>
      [...handlers.values()].reduce((n, set) => n + set.size, 0),
  }
}

let win: ReturnType<typeof fakeTarget>
let doc: ReturnType<typeof fakeTarget>
let vv: ReturnType<typeof fakeTarget>

beforeEach(() => {
  vi.useFakeTimers()
  win = fakeTarget()
  doc = fakeTarget()
  vv = fakeTarget()
  // rAF is a frame-scheduler here; the timers carry the rest.
  vi.stubGlobal('requestAnimationFrame', (fn: () => void) => {
    return setTimeout(fn, 16) as unknown as number
  })
  vi.stubGlobal('window', {
    ...win,
    setTimeout: (fn: () => void, ms: number) => setTimeout(fn, ms),
    clearTimeout: (id: number) => clearTimeout(id),
  })
  vi.stubGlobal('document', doc)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('subscribeToViewport', () => {
  it('recovers when the keyboard-dismiss resize never arrives', () => {
    const update = vi.fn()
    subscribeToViewport(vv as unknown as VisualViewport, update)
    update.mockClear()

    // The keyboard goes away. iOS fires focusout and then — this is the bug — no resize at all.
    win.emit('focusout')
    vi.advanceTimersByTime(500)

    expect(
      update.mock.calls.length,
      'nothing re-read the viewport after the keyboard left, so the shell stays sized for it — ' +
        'the app renders into part of the screen and the rest shows page background',
    ).toBeGreaterThan(0)
  })

  it('re-reads after the dismiss animation, not only during it', () => {
    const update = vi.fn()
    subscribeToViewport(vv as unknown as VisualViewport, update)
    update.mockClear()

    win.emit('focusout')
    vi.advanceTimersByTime(50); // a couple of frames in: still mid-animation
    const early = update.mock.calls.length
    vi.advanceTimersByTime(450); // past the end of it

    expect(
      early,
      'should read straight away for the fast path',
    ).toBeGreaterThan(0)
    expect(
      update.mock.calls.length,
      'must read AGAIN once the animation is over: the early value is the mid-animation height, ' +
        'which is exactly the wrong one to keep',
    ).toBeGreaterThan(early)
  })

  it('still listens to the viewport directly, for the case that does work', () => {
    const update = vi.fn()
    subscribeToViewport(vv as unknown as VisualViewport, update)
    update.mockClear()

    vv.emit('resize')
    expect(update).toHaveBeenCalledTimes(1)
    vv.emit('scroll')
    expect(update).toHaveBeenCalledTimes(2)
  })

  it('covers returning to a backgrounded PWA, which resumes at a stale size', () => {
    const update = vi.fn()
    subscribeToViewport(vv as unknown as VisualViewport, update)
    update.mockClear()

    doc.emit('visibilitychange')
    vi.advanceTimersByTime(500)
    expect(update.mock.calls.length).toBeGreaterThan(0)
  })

  it('unsubscribes everything, including pending timers', () => {
    const update = vi.fn()
    const stop = subscribeToViewport(vv as unknown as VisualViewport, update)

    win.emit('focusout'); // leaves a timer in flight
    stop()
    update.mockClear()

    vi.advanceTimersByTime(1000)
    vv.emit('resize')
    win.emit('focusout')

    expect(
      update,
      'a torn-down subscription must not keep firing',
    ).not.toHaveBeenCalled()
    expect(vv.listenerCount() + win.listenerCount() + doc.listenerCount()).toBe(
      0,
    )
  })
})
