import { describe, expect, it } from 'vitest'
import { generateRecoveryCode, normalizeRecoveryCode } from './mls'

describe('recovery codes', () => {
  // THE BUG THIS PINS. A backup is sealed under the NORMALISED code, and the code shown to the user
  // is the pretty one — grouped with dashes. If those two are not the same bytes, a restore cannot
  // open a backup the app itself has just made. Both clients had exactly that asymmetry, hidden
  // behind a retry that tried the normalised form after the raw one threw.
  it('a generated code is not its own normalised form', () => {
    const pretty = generateRecoveryCode()
    expect(pretty).toContain('-')
    expect(normalizeRecoveryCode(pretty)).not.toBe(pretty)
  })

  it('generates a 25-character code in five groups', () => {
    const code = generateRecoveryCode()
    expect(code.split('-')).toHaveLength(5)
    for (const group of code.split('-')) expect(group).toHaveLength(5)
    expect(normalizeRecoveryCode(code)).toHaveLength(25)
  })

  it('never repeats itself', () => {
    const seen = new Set(Array.from({ length: 50 }, () => generateRecoveryCode()))
    expect(seen.size).toBe(50)
  })

  // Everything a human might do to a code on the way back in must land on the same bytes, or the
  // backup will not open for someone who typed it correctly by eye.
  it('normalises the ways a person retypes a code', () => {
    const canonical = normalizeRecoveryCode('ABCDE-FGH23-45678-9JKMN-PQRST')
    const variants = [
      'abcde-fgh23-45678-9jkmn-pqrst', // lower case
      'ABCDE FGH23 45678 9JKMN PQRST', // spaces instead of dashes
      'ABCDEFGH23456789JKMNPQRST', // run together
      '  ABCDE-FGH23-45678-9JKMN-PQRST  ', // padded
    ]
    for (const v of variants) {
      expect(normalizeRecoveryCode(v), `variant ${v}`).toBe(canonical)
    }
  })

  // The ambiguous glyphs. Somebody reading a code off a screen cannot tell these apart, so the
  // alphabet excludes them and the normaliser folds them in.
  it('folds the characters a reader cannot tell apart', () => {
    expect(normalizeRecoveryCode('I')).toBe('1')
    expect(normalizeRecoveryCode('L')).toBe('1')
    expect(normalizeRecoveryCode('l')).toBe('1')
    expect(normalizeRecoveryCode('O')).toBe('0')
    expect(normalizeRecoveryCode('o')).toBe('0')
  })

  // Applying it twice must change nothing, or a client that normalises before calling a function
  // that also normalises would derive a different key.
  it('is idempotent', () => {
    const code = generateRecoveryCode()
    const once = normalizeRecoveryCode(code)
    expect(normalizeRecoveryCode(once)).toBe(once)
  })

  it('drops anything that is not part of a code', () => {
    expect(normalizeRecoveryCode('AB!@#CD')).toBe('ABCD')
    expect(normalizeRecoveryCode('')).toBe('')
  })

  // A generated code must survive its own normaliser without losing entropy — if the alphabet
  // contained I, L or O, normalising would collapse distinct codes onto the same bytes.
  it('generated codes never collide after normalisation', () => {
    const normalised = new Set(
      Array.from({ length: 100 }, () => normalizeRecoveryCode(generateRecoveryCode())),
    )
    expect(normalised.size).toBe(100)
  })
})
