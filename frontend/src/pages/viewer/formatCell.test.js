import { expect, it } from 'vitest'
import { formatCell } from './formatCell.js'

it('format nilai sel tabel', () => {
  expect(formatCell(null)).toEqual({ text: 'NULL', isNull: true })
  expect(formatCell('NULL')).toEqual({ text: 'NULL', isNull: false })
  expect(formatCell(0).text).toBe('0')
  expect(formatCell(false).text).toBe('false')
  expect(formatCell({ a: [1, 2] }).text).toBe('{"a":[1,2]}')
  expect(formatCell('\\x0aff').text).toBe('\\x0aff')
})
