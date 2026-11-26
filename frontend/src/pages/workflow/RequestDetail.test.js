import { expect, it } from 'vitest'
import { pendingGroupNames } from './RequestDetail.jsx'

it('pending_group_ids dari error 409 diubah jadi nama group', () => {
  const request = { approvals: [{ group_id: '1', group_name: 'analyst' }, { group_id: '2', group_name: 'developer' }] }
  expect(pendingGroupNames(request, ['2', '9'])).toBe('developer, Group #9')
})
