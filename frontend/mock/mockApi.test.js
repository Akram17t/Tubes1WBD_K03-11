import { expect, it } from 'vitest'
import { hideRollback, positions } from './mockApi.js'

const e = (id, operation, status = 'success', target = null) => ({ id, migration_id: id === '1' ? 'A' : id === '5' ? 'C' : 'B', operation, status, target_execution_id: target })

it('Hide rollback sesuai contoh wajib kontrak 10', () => {
  const history = [e('1', 'up'), e('2', 'up'), e('3', 'rollback', 'success', '2'), e('4', 'forward', 'success', '2'), e('5', 'up')]
  expect(hideRollback(history).map((x) => x.id)).toEqual(['1', '4', '5'])
  // rollback gagal -> E2 dan E3 tetap kelihatan
  history[2].status = 'failed'
  expect(hideRollback(history).map((x) => x.id)).toEqual(['1', '2', '3', '4', '5'])
})

it('active/forward stack: rollback pindah ke forward, up baru buang forward lama', () => {
  const { active, forward } = positions([e('1', 'up'), e('2', 'up'), e('3', 'rollback', 'success', '2')])
  expect(active.map((x) => x.id)).toEqual(['1'])
  expect(forward.map((x) => x.id)).toEqual(['2'])
  expect(positions([e('1', 'up'), e('2', 'up'), e('3', 'rollback', 'success', '2'), e('5', 'up')]).forward).toEqual([])
})
