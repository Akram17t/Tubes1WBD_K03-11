import { useState } from 'react'
import { api } from '../../api/client.js'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

const MAX = 500
const LEVELS = [
  { value: 'view', label: 'View (lihat saja)' },
  { value: 'edit', label: 'Edit (boleh ajukan migration)' },
]

// akses database milik group: tambah grant, ubah level (PUT idempotent), cabut grant
export function GroupGrants({ groupId }) {
  const grants = useApi((signal) => api.get(`/admin/groups/${groupId}/access`, { limit: MAX }, { signal }), [groupId])
  const connections = useApi((signal) => api.get('/admin/connections', { limit: MAX }, { signal }), [])
  const [form, setForm] = useState({ database_id: '', access_level: 'view' })
  const action = useAction()

  const granted = new Set((grants.data || []).map((g) => g.database_id))
  const candidates = (connections.data || []).filter((c) => c.is_active && !granted.has(c.id))

  const save = (databaseId, level) =>
    action
      .run(async () => {
        await api.put(`/admin/groups/${groupId}/access/${databaseId}`, { access_level: level })
        grants.reload()
      })
      .then(() => true, () => false)

  const revoke = (databaseId) =>
    action.run(async () => {
      await api.del(`/admin/groups/${groupId}/access/${databaseId}`)
      grants.reload()
    })

  const handleAdd = (event) => {
    event.preventDefault()
    if (!form.database_id) return
    save(form.database_id, form.access_level).then((ok) => ok && setForm((f) => ({ ...f, database_id: '' })))
  }

  return (
    <section className="card mb-4">
      <div className="card-body">
        <h2 className="h5">Akses database</h2>
        <p className="small text-secondary">Menurunkan atau mencabut akses langsung mencabut session anggota; menaikkan akses merotasi session.</p>
        <ErrorAlert error={action.error} />

        <form className="row g-2 mb-3" onSubmit={handleAdd}>
          <div className="col-sm-6 col-md-5">
            <label htmlFor="grant-db" className="visually-hidden">
              Database
            </label>
            <select
              id="grant-db"
              className="form-select"
              value={form.database_id}
              disabled={connections.loading}
              onChange={(e) => setForm((f) => ({ ...f, database_id: e.target.value }))}
            >
              <option value="">{connections.loading ? 'Memuat database...' : candidates.length ? 'Pilih database' : 'Tidak ada database lain'}</option>
              {candidates.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>
          <div className="col-sm-6 col-md-4">
            <label htmlFor="grant-level" className="visually-hidden">
              Tingkat akses
            </label>
            <select id="grant-level" className="form-select" value={form.access_level} onChange={(e) => setForm((f) => ({ ...f, access_level: e.target.value }))}>
              {LEVELS.map((l) => (
                <option key={l.value} value={l.value}>
                  {l.label}
                </option>
              ))}
            </select>
          </div>
          <div className="col-md-3 d-grid">
            <button type="submit" className="btn btn-outline-primary" disabled={!form.database_id || action.busy}>
              Beri akses
            </button>
          </div>
        </form>

        {grants.loading && !grants.data ? (
          <Loading />
        ) : grants.error ? (
          <ErrorAlert error={grants.error} onRetry={grants.reload} />
        ) : grants.data.length === 0 ? (
          <Empty text="Group ini belum punya akses ke database mana pun." />
        ) : (
          <div className="table-responsive">
            <table className="table align-middle mb-0">
              <thead>
                <tr>
                  <th>Database</th>
                  <th>Tingkat akses</th>
                  <th className="text-end">Aksi</th>
                </tr>
              </thead>
              <tbody>
                {grants.data.map((g) => (
                  <tr key={g.database_id}>
                    <td>
                      {g.database_name}
                      {!g.is_active && <span className="badge text-bg-secondary ms-2">Koneksi nonaktif</span>}
                    </td>
                    <td style={{ minWidth: '12rem' }}>
                      <select
                        className="form-select form-select-sm"
                        aria-label={`Tingkat akses ${g.database_name}`}
                        value={g.access_level}
                        disabled={action.busy}
                        onChange={(e) => save(g.database_id, e.target.value)}
                      >
                        {LEVELS.map((l) => (
                          <option key={l.value} value={l.value}>
                            {l.label}
                          </option>
                        ))}
                      </select>
                    </td>
                    <td className="text-end">
                      <ConfirmButton busy={action.busy} onConfirm={() => revoke(g.database_id)} confirmText="Cabut">
                        Cabut
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
