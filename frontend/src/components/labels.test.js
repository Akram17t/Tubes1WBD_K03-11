import { expect, it } from 'vitest'
import { actorName, formatTime } from './labels.js'

it('formatTime aman buat null dan string aneh', () => {
  expect(formatTime(null)).toBe('—')
  expect(formatTime('bukan-tanggal')).toBe('bukan-tanggal')
  expect(formatTime('2026-11-24T08:00:00Z')).toMatch(/2026/)
})

it('actorName nandain user login sebagai Kamu', () => {
  expect(actorName('1', { id: '1' })).toBe('Kamu')
  expect(actorName('7', { id: '1' })).toBe('User #7')
  expect(actorName(null, { id: '1' })).toBe('—')
})
