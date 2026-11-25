import { Link, useOutletContext } from 'react-router'
import { api } from '../../api/client.js'
import { PositionBadge } from '../../components/Badges.jsx'
import { formatTime } from '../../components/labels.js'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// daftar definisi migration di satu database. Tombol buat cuma buat akses Edit
export function Migrations() {
  const { database } = useOutletContext()
  const [offset, setOffset] = useOffset()
  const migrations = useApi((signal) => api.get(`/databases/${database.id}/migrations`, { limit: PAGE_LIMIT, offset }, { signal }), [database.id, offset])

  if (migrations.loading && !migrations.data) return <Loading />
  if (migrations.error) return <ErrorAlert error={migrations.error} onRetry={migrations.reload} />

  const base = `/databases/${database.id}/migrations`
  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <p className="small text-secondary mb-0 me-auto">
          Definisi berisi SQL up/down. Definisi dikunci setelah dipakai request, perubahan berikutnya jadi revisi baru.
        </p>
        {database.access_level === 'edit' && (
          <Link to={`${base}/new`} className="btn btn-primary btn-sm">
            Buat migration
          </Link>
        )}
      </div>
      {migrations.data.length === 0 ? (
        <Empty text="Belum ada definisi migration di database ini." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white">
            <thead>
              <tr>
                <th>#</th>
                <th>Judul</th>
                <th>Mode</th>
                <th>Posisi</th>
                <th>Diubah</th>
              </tr>
            </thead>
            <tbody>
              {migrations.data.map((m) => (
                <tr key={m.id}>
                  <td className="font-monospace">
                    <Link to={`${base}/${m.id}`}>#{m.id}</Link>
                  </td>
                  <td>
                    <Link to={`${base}/${m.id}`} className="text-decoration-none text-body">
                      {m.title}
                    </Link>
                    <span className="text-secondary small ms-1">rev {m.revision}</span>
                    {m.irreversible && <span className="badge text-bg-danger ms-1">Irreversible</span>}
                    {m.locked_at && <span className="badge text-bg-light border ms-1">Terkunci</span>}
                  </td>
                  <td className="small">{m.mode}</td>
                  <td>
                    <PositionBadge position={m.position} />
                  </td>
                  <td className="small text-nowrap">{formatTime(m.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={migrations.meta} offset={offset} onChange={setOffset} count={migrations.data.length} />
    </>
  )
}
