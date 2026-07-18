import { describe, expect, it } from 'vitest'
import { parseMembership } from './mls'

const bytes = (value: unknown): Uint8Array =>
  new TextEncoder().encode(typeof value === 'string' ? value : JSON.stringify(value))

describe('parseMembership', () => {
  it('parses every action the server writes', () => {
    for (const action of ['added', 'removed', 'left'] as const) {
      const event = parseMembership(bytes({ action, actorId: 'a', userId: 'u' }))
      expect(event).not.toBeNull()
      expect(event?.action).toBe(action)
      expect(event?.actorId).toBe('a')
      expect(event?.userId).toBe('u')
    }
  })

  it('refuses an unknown action rather than rendering nonsense', () => {
    expect(parseMembership(bytes({ action: 'promoted', userId: 'u' }))).toBeNull()
  })

  it('refuses a note with nobody in it', () => {
    expect(parseMembership(bytes({ action: 'added' }))).toBeNull()
    expect(parseMembership(bytes({ action: 'added', userId: '' }))).toBeNull()
  })

  // A conversation must not fail to render because of a note ABOUT it.
  it('survives anything that is not a membership note', () => {
    for (const input of [bytes(''), bytes('not json'), bytes([]), bytes('"a string"'), new Uint8Array([0xff, 0xfe])]) {
      expect(parseMembership(input)).toBeNull()
    }
  })

  it('tolerates a missing actor', () => {
    const event = parseMembership(bytes({ action: 'left', userId: 'u' }))
    expect(event?.actorId).toBe('')
  })
})
