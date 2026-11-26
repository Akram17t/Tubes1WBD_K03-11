import { Link } from 'react-router'
import { api } from '../../api/client.js'
import { PageHeader, StatusBadge } from '../../components/PageHeader.jsx'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'
import { TestConnectionButton } from './TestConnectionButton.jsx'

// daftar koneksi database target. Password ga pernah dikirim server, jadi ga ditampilin sama sekali
export function Connections() {
  const [offset, setOffset] = useOffset()
  const connections = useApi((signal) => api.get('/admin/connections', { limit: PAGE_LIMIT, offset }, { signal }), [offset])

  return (
    <>
      <PageHeader title="Koneksi database">
        <Link to="/admin/connections/new" className="btn btn-primary">
          Tambah koneksi
        </Link>
      </PageHeader>
      {connections.loading && !connections.data ? (
        <Loading />
      ) : connections.error ? (
        <ErrorAlert error={connections.error} onRetry={connections.reload} />
      ) : connections.data.length === 0 ? (
        <Empty text="Belum ada koneksi database." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white">
            <thead>
              <tr>
                <th>Nama</th>
                <th>Server</th>
                <th>Database</th>
                <th>SSL</th>
                <th>Status</th>
                <th className="text-end">Aksi</th>
              </tr>
            </thead>
            <tbody>
              {connections.data.map((c) => (
                <tr key={c.id}>
                  <td className="fw-semibold">{c.name}</td>
                  <td className="font-monospace small">
                    {c.host}:{c.port}
                  </td>
                  <td className="font-monospace small">
                    {c.database_name} <span className="text-secondary">as {c.db_username}</span>
                  </td>
                  <td className="small">{c.ssl_mode}</td>
                  <td>
                    <StatusBadge active={c.is_active} />
                  </td>
                  <td className="text-end text-nowrap">
                    <TestConnectionButton connectionId={c.id} disabled={!c.is_active} />
                    <Link to={`/admin/connections/${c.id}/edit`} className="btn btn-sm btn-outline-primary ms-1">
                      Edit
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={connections.meta} offset={offset} onChange={setOffset} count={connections.data?.length} />
    </>
  )
}
