// What counts as a server address, and what it becomes once stored.
//
// This is the string an operator reads out and a person types on the sign-in form before they have
// an account, so getting it wrong locks somebody out of a server that is working perfectly well.
// The rule is pure precisely so it can be pinned here rather than discovered in a browser, and it
// must stay identical to the mobile one — see mobile/test/unit/server_url_test.dart. The same
// address typed into the two apps has to reach the same place.

import { describe, expect, it } from 'vitest'
import { isValidServerUrl, normalizeServerUrl } from './server'

describe('normalizeServerUrl accepts what people actually type', () => {
  it('gives a bare hostname https', () => {
    expect(normalizeServerUrl('pheme.example.com')).toBe('https://pheme.example.com')
  })

  it('keeps the unlisted path prefix on a bare hostname', () => {
    // The form a self-host operator hands over, and the reason the scheme cannot be required.
    expect(normalizeServerUrl('host.example/a7f3c91e4b2d')).toBe('https://host.example/a7f3c91e4b2d')
  })

  it('leaves an explicit scheme alone', () => {
    expect(normalizeServerUrl('https://pheme.example')).toBe('https://pheme.example')
  })

  it('lets http survive, because a local backend is a deliberate act', () => {
    expect(normalizeServerUrl('http://10.0.2.2:8080')).toBe('http://10.0.2.2:8080')
  })

  it('reads a host:port as a host and a port, not a scheme', () => {
    // `10.0.2.2:8080` looks like scheme+path to a naive parser. A scheme starts with a LETTER.
    expect(normalizeServerUrl('10.0.2.2:8080')).toBe('https://10.0.2.2:8080')
    expect(normalizeServerUrl('host.example:8443')).toBe('https://host.example:8443')
  })

  it('forgives surrounding whitespace', () => {
    expect(normalizeServerUrl('  pheme.example  ')).toBe('https://pheme.example')
  })

  it('drops a trailing slash', () => {
    // A pasted browser URL brings one along, and joining "/v1/..." onto it yields "//v1/...",
    // which fails invisibly.
    expect(normalizeServerUrl('https://pheme.example/')).toBe('https://pheme.example')
    expect(normalizeServerUrl('pheme.example/prefix/')).toBe('https://pheme.example/prefix')
  })
})

describe('normalizeServerUrl refuses what cannot be a server', () => {
  it('refuses nothing at all', () => {
    expect(normalizeServerUrl('')).toBeNull()
    expect(normalizeServerUrl('   ')).toBeNull()
  })

  it('refuses a scheme we cannot speak', () => {
    expect(normalizeServerUrl('ftp://pheme.example')).toBeNull()
    expect(normalizeServerUrl('wss://pheme.example')).toBeNull()
  })

  it('refuses a scheme with no host behind it', () => {
    expect(normalizeServerUrl('https://')).toBeNull()
    expect(normalizeServerUrl('https:///a/path')).toBeNull()
  })
})

describe('isValidServerUrl agrees with it', () => {
  it('accepts a bare hostname', () => {
    expect(isValidServerUrl('pheme.example.com')).toBe(true)
  })

  it('rejects an empty field', () => {
    expect(isValidServerUrl('')).toBe(false)
  })
})
