import { Link } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { PageHeader, StatusBadge } from '../../components/PageHeader.jsx'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

// daftar user + tombol aktif/nonaktif. Nonaktifin akun sendiri ga ditawarin (backend juga nolak)
export function Users() {
  const { user: me } = useAuth()
  const [offset, setOffset] = useOffset()
  const users = useApi((signal) => api.get('/admin/users', { limit: PAGE_LIMIT, offset }, { signal }), [offset])
  const action = useAction()

  // ganti status aktif lewat PATCH is_active, lalu muat ulang daftar
  const setActive = (u, active) =>
    action.run(async () => {
      await api.patch(`/admin/users/${u.id}`, { is_active: active })
      users.reload()
    })

  return (
    <>
      <PageHeader title="User">
        <Link to="/admin/users/new" className="btn btn-primary">
          Tambah user
        </Link>
      </PageHeader>
      <p className="text-secondary small">
        Menonaktifkan user langsung mencabut semua session-nya dan melepas jabatan leader group yang dipegangnya.
      </p>
      <ErrorAlert error={action.error} />
      {users.loading && !users.data ? (
        <Loading />
      ) : users.error ? (
        <ErrorAlert error={users.error} onRetry={users.reload} />
      ) : users.data.length === 0 ? (
        <Empty text="Belum ada user." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white">
            <thead>
              <tr>
                <th>Username</th>
                <th>Nama tampilan</th>
                <th>Peran</th>
                <th>Status</th>
                <th className="text-end">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {users.data.map((u) => (
                <tr key={u.id}>
                  <td className="font-monospace">{u.username}</td>
                  <td>
                    {u.display_name}
                    {u.id === me.id && <span className="badge text-bg-info ms-2">Kamu</span>}
                  </td>
                  <td>{u.is_platform_admin ? <span className="badge text-bg-warning">Admin</span> : 'User'}</td>
                  <td>
                    <StatusBadge active={u.is_active} />
                  </td>
                  <td className="text-end text-nowrap">
                    <Link to={`/admin/users/${u.id}/edit`} className="btn btn-sm btn-outline-primary me-1">
                      Edit
                    </Link>
                    {u.is_active ? (
                      u.id !== me.id && (
                        <ConfirmButton busy={action.busy} onConfirm={() => setActive(u, false)} confirmText="Nonaktifkan">
                          Nonaktifkan
                        </ConfirmButton>
                      )
                    ) : (
                      <button
                        type="button"
                        className="btn btn-sm btn-outline-success"
                        disabled={action.busy}
                        onClick={() => setActive(u, true).catch(() => {})}
                      >
                        Aktifkan
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={users.meta} offset={offset} onChange={setOffset} count={users.data?.length} />
    </>
  )
}
