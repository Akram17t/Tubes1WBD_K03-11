import { expect, it } from 'vitest'
import { safeNext } from './safeNext.js'

it('cuma nerima path internal', () => {
  expect(safeNext('/databases/1?tab=data')).toBe('/databases/1?tab=data')
  for (const bad of [null, '', 'https://evil.com', '//evil.com', '/\\evil.com', 'javascript:alert(1)']) {
    expect(safeNext(bad)).toBe('/')
  }
})
