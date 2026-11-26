import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api } from '../../api/client.js'
import { PageHeader } from '../../components/PageHeader.jsx'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

// daftar group aktif + form singkat bikin group baru (leader diatur setelah ada anggota)
export function Groups() {
  const navigate = useNavigate()
  const [offset, setOffset] = useOffset()
  const groups = useApi((signal) => api.get('/admin/groups', { limit: PAGE_LIMIT, offset }, { signal }), [offset])
  const [form, setForm] = useState({ name: '', description: '' })
  const action = useAction()

  // bikin group lalu langsung buka detailnya buat nambah anggota
  async function handleCreate(event) {
    event.preventDefault()
    try {
      const { data } = await action.run(() =>
        api.post('/admin/groups', { name: form.name, description: form.description.trim() || null }),
      )
      navigate(`/admin/groups/${data.id}`)
    } catch {
      /* error tampil lewat action.error */
    }
  }

  return (
    <>
      <PageHeader title="Group" />
      <div className="card mb-4">
        <form className="card-body row g-2 align-items-end" onSubmit={handleCreate}>
          <div className="col-md-4">
            <label htmlFor="group-name" className="form-label">
              Nama group baru
            </label>
            <input
              id="group-name"
              className="form-control"
              required
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div className="col-md-6">
            <label htmlFor="group-desc" className="form-label">
              Deskripsi (opsional)
            </label>
            <input
              id="group-desc"
              className="form-control"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
            />
          </div>
          <div className="col-md-2 d-grid">
            <button type="submit" className="btn btn-primary" disabled={action.busy}>
              {action.busy ? 'Membuat...' : 'Buat group'}
            </button>
          </div>
          {action.error && (
            <div className="col-12">
              <ErrorAlert error={action.error} />
            </div>
          )}
        </form>
      </div>

      {groups.loading && !groups.data ? (
        <Loading />
      ) : groups.error ? (
        <ErrorAlert error={groups.error} onRetry={groups.reload} />
      ) : groups.data.length === 0 ? (
        <Empty text="Belum ada group." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white">
            <thead>
              <tr>
                <th>Nama</th>
                <th>Deskripsi</th>
                <th>Leader</th>
                <th className="text-end">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {groups.data.map((g) => (
                <tr key={g.id}>
                  <td className="fw-semibold">{g.name}</td>
                  <td className="text-secondary">{g.description || '—'}</td>
                  <td>
                    {g.leader_user_id ? (
                      <span className="badge text-bg-success">Ada leader</span>
                    ) : (
                      <span className="badge text-bg-warning">Belum ada leader</span>
                    )}
                  </td>
                  <td className="text-end">
                    <Link to={`/admin/groups/${g.id}`} className="btn btn-sm btn-outline-primary">
                      Kelola
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={groups.meta} offset={offset} onChange={setOffset} count={groups.data?.length} />
    </>
  )
}
