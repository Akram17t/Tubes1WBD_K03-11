import { useState } from 'react'
import { api } from '../../api/client.js'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { StatusBadge } from '../../components/PageHeader.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

const MAX = 500

// anggota group: tambah/hapus anggota, tunjuk atau lepas leader (leader wajib anggota)
export function GroupMembers({ group, onLeaderChanged }) {
  const members = useApi((signal) => api.get(`/admin/groups/${group.id}/members`, { limit: MAX }, { signal }), [group.id, group.leader_user_id])
  const users = useApi((signal) => api.get('/admin/users', { limit: MAX }, { signal }), [])
  const [selected, setSelected] = useState('')
  const action = useAction()

  const memberIds = new Set((members.data || []).map((m) => m.id))
  const candidates = (users.data || []).filter((u) => u.is_active && !memberIds.has(u.id))

  // semua aksi di sini ujungnya muat ulang anggota (+ group kalau leader berubah); balikin true kalau sukses
  const act = (fn, leaderChanged = false) =>
    action
      .run(async () => {
        await fn()
        if (leaderChanged) onLeaderChanged()
        else members.reload()
      })
      .then(() => true, () => false)

  const addMember = (event) => {
    event.preventDefault()
    if (!selected) return
    act(() => api.post(`/admin/groups/${group.id}/members`, { user_id: selected })).then((ok) => ok && setSelected(''))
  }
  const setLeader = (userId) => act(() => api.patch(`/admin/groups/${group.id}`, { leader_user_id: userId }), true)
  const removeMember = (m) => act(() => api.del(`/admin/groups/${group.id}/members/${m.id}`), m.is_leader)

  return (
    <section className="card mb-4">
      <div className="card-body">
        <h2 className="h5">Anggota</h2>
        <p className="small text-secondary">
          Leader group memberi approval migration atas nama group-nya. Menambah atau menghapus anggota memperbarui session user terkait.
        </p>
        {!group.leader_user_id && (
          <div className="alert alert-warning py-2">Group ini belum punya leader. Request migration yang butuh approval group ini akan tertahan.</div>
        )}
        <ErrorAlert error={action.error} />

        <form className="row g-2 mb-3" onSubmit={addMember}>
          <div className="col-sm-8 col-md-6">
            <label htmlFor="add-member" className="visually-hidden">
              Pilih user
            </label>
            <select id="add-member" className="form-select" value={selected} onChange={(e) => setSelected(e.target.value)} disabled={users.loading}>
              <option value="">{users.loading ? 'Memuat user...' : candidates.length ? 'Pilih user untuk ditambahkan' : 'Semua user aktif sudah jadi anggota'}</option>
              {candidates.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.display_name} ({u.username})
                </option>
              ))}
            </select>
          </div>
          <div className="col-sm-4 col-md-2 d-grid">
            <button type="submit" className="btn btn-outline-primary" disabled={!selected || action.busy}>
              Tambah
            </button>
          </div>
        </form>

        {members.loading && !members.data ? (
          <Loading />
        ) : members.error ? (
          <ErrorAlert error={members.error} onRetry={members.reload} />
        ) : members.data.length === 0 ? (
          <Empty text="Belum ada anggota." />
        ) : (
          <div className="table-responsive">
            <table className="table align-middle mb-0">
              <thead>
                <tr>
                  <th>Nama</th>
                  <th>Status</th>
                  <th className="text-end">Aksi</th>
                </tr>
              </thead>
              <tbody>
                {members.data.map((m) => (
                  <tr key={m.id}>
                    <td>
                      {m.display_name} <span className="text-secondary font-monospace small">({m.username})</span>
                      {m.is_leader && <span className="badge text-bg-primary ms-2">Leader</span>}
                    </td>
                    <td>
                      <StatusBadge active={m.is_active} />
                    </td>
                    <td className="text-end text-nowrap">
                      {m.is_leader ? (
                        <button type="button" className="btn btn-sm btn-outline-secondary me-1" disabled={action.busy} onClick={() => setLeader(null)}>
                          Lepas leader
                        </button>
                      ) : (
                        m.is_active && (
                          <button type="button" className="btn btn-sm btn-outline-primary me-1" disabled={action.busy} onClick={() => setLeader(m.id)}>
                            Jadikan leader
                          </button>
                        )
                      )}
                      <ConfirmButton busy={action.busy} onConfirm={() => removeMember(m)} confirmText="Keluarkan">
                        Keluarkan
                      </ConfirmButton>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}
