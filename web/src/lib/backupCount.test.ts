// The count the server compares one backup against another by.
//
// The server cannot open the seal, so this number is the ONLY thing it has to tell a device that
// has read everything from one that has read nothing. That matters because there is one backup per
// user and an upload replaces it: a device holding nothing must not be able to overwrite a full
// history, and the shrink guard is what stops it.
//
// This client sent no count at all. Every upload therefore described itself as holding zero
// messages, the guard refused each one with a 409, and the auto-backup path swallowed the error —
// so a browser that had backed up nothing for months looked exactly like one that was working.

import { describe, expect, it } from 'vitest'
import { countBodies } from './chatCache'

describe('countBodies', () => {
  it('counts every body across every conversation', () => {
    expect(
      countBodies({
        c1: { m1: 'a', m2: 'b' },
        c2: { m3: 'c' },
      }),
    ).toBe(3)
  })

  it('an empty transcript counts zero, not one', () => {
    expect(countBodies({})).toBe(0)
    // A conversation present but empty must not inflate the count. An overstated count is what
    // would let an empty backup past the guard that exists to refuse exactly that.
    expect(countBodies({ c1: {} })).toBe(0)
  })

  it('grows with the transcript, never independently', () => {
    const transcript: Record<string, Record<string, string>> = { c1: { m1: 'a' } }
    expect(countBodies(transcript)).toBe(1)
    transcript.c1.m2 = 'b'
    expect(countBodies(transcript)).toBe(2)
    transcript.c2 = { m3: 'c' }
    expect(countBodies(transcript)).toBe(3)
  })

  // The number has to describe the blob that travels with it. A count of "what this device holds"
  // sent alongside a transcript that was dropped for being too large would tell the server this
  // upload carries a history it does not carry — and the guard would then let it replace one.
  it('is the number of bodies, not the number of conversations', () => {
    expect(countBodies({ c1: { m1: 'a' }, c2: { m2: 'b' }, c3: { m3: 'c' } })).toBe(3)
    expect(countBodies({ c1: { m1: 'a', m2: 'b', m3: 'c' } })).toBe(3)
  })
})
