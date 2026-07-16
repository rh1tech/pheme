// The interesting cases are the failures, not the happy path: a version check that cries wolf on
// every flaky poll is worse than no version check, because people learn to ignore it.

import { describe, expect, it } from 'vitest'
import { fetchDeployedBuildId, isOutdated } from './version'

function reply(body: unknown, ok = true): typeof fetch {
  return (async () =>
    ({
      ok,
      json: async () => body,
    }) as Response) as unknown as typeof fetch
}

describe('isOutdated', () => {
  it('is out of date when the deployed id differs', () => {
    expect(isOutdated('b2', 'b1')).toBe(true)
  })

  it('is current when the ids match', () => {
    expect(isOutdated('b1', 'b1')).toBe(false)
  })

  // Not being able to ask is not evidence of anything. This is the one that keeps the prompt from
  // appearing every time the network hiccups.
  it('never claims out of date when the deployed id is unknown', () => {
    expect(isOutdated(null, 'b1')).toBe(false)
  })
})

describe('fetchDeployedBuildId', () => {
  it('reads the id', async () => {
    expect(await fetchDeployedBuildId(reply({ buildId: 'abc' }))).toBe('abc')
  })

  it('is unknown on a non-OK response — an error page is not a version', async () => {
    expect(await fetchDeployedBuildId(reply({ buildId: 'abc' }, false))).toBeNull()
  })

  it('is unknown when the network throws', async () => {
    const boom = (async () => {
      throw new Error('offline')
    }) as unknown as typeof fetch
    expect(await fetchDeployedBuildId(boom)).toBeNull()
  })

  // A proxy or captive portal happily serves HTML with a 200. Parsing that must not mint a
  // "version" that then differs from ours forever.
  it('is unknown when the body is not JSON', async () => {
    const html = (async () =>
      ({
        ok: true,
        json: async () => {
          throw new SyntaxError('Unexpected token <')
        },
      }) as unknown as Response) as unknown as typeof fetch
    expect(await fetchDeployedBuildId(html)).toBeNull()
  })

  it('is unknown when the body has no usable id', async () => {
    expect(await fetchDeployedBuildId(reply({}))).toBeNull()
    expect(await fetchDeployedBuildId(reply({ buildId: '' }))).toBeNull()
    expect(await fetchDeployedBuildId(reply({ buildId: 42 }))).toBeNull()
    expect(await fetchDeployedBuildId(reply(null))).toBeNull()
  })
})
