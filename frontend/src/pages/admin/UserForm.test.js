import { expect, it } from 'vitest'
import { changedFields } from './UserForm.jsx'

it('PATCH user cuma bawa field yang berubah, password kosong ga dikirim', () => {
  const original = { username: 'budi', display_name: 'Budi', is_platform_admin: false }
  expect(changedFields(original, { ...original, password: '' })).toEqual({})
  expect(changedFields(original, { ...original, display_name: 'Budi S', password: '' })).toEqual({ display_name: 'Budi S' })
  expect(changedFields(original, { ...original, is_platform_admin: true, password: 'rahasia123' })).toEqual({
    is_platform_admin: true,
    password: 'rahasia123',
  })
})
