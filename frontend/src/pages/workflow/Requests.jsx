import { Link, useOutletContext } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { OperationBadge, ReviewStatusBadge } from '../../components/Badges.jsx'
import { actorName, formatTime } from '../../components/labels.js'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// daftar request migration di satu database, terbaru di atas
export function Requests() {
  const { database } = useOutletContext()
  const { user } = useAuth()
  const [offset, setOffset] = useOffset()
  const requests = useApi((signal) => api.get(`/databases/${database.id}/requests`, { limit: PAGE_LIMIT, offset }, { signal }), [database.id, offset])

  if (requests.loading && !requests.data) return <Loading />
  if (requests.error) return <ErrorAlert error={requests.error} onRetry={requests.reload} />

  return (
    <>
      <p className="small text-secondary">
        Request baru dibuat dari halaman Migration. Setelah di-submit, leader setiap group yang punya akses ke database ini memberi approval,
        lalu pengaju menekan tombol eksekusi secara terpisah.
      </p>
      {requests.data.length === 0 ? (
        <Empty text="Belum ada request migration di database ini." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white">
            <thead>
              <tr>
                <th>#</th>
                <th>Migration</th>
                <th>Operasi</th>
                <th>Status</th>
                <th>Pengaju</th>
                <th>Dibuat</th>
              </tr>
            </thead>
            <tbody>
              {requests.data.map((r) => (
                <tr key={r.id}>
                  <td className="font-monospace">
                    <Link to={`/databases/${database.id}/requests/${r.id}`}>#{r.id}</Link>
                  </td>
                  <td>
                    <Link to={`/databases/${database.id}/requests/${r.id}`} className="text-decoration-none text-body">
                      {r.snapshot?.title || `Migration #${r.migration_id}`}
                    </Link>
                    <span className="text-secondary small ms-1">rev {r.revision}</span>
                  </td>
                  <td>
                    <OperationBadge operation={r.operation} />
                  </td>
                  <td>
                    <ReviewStatusBadge status={r.review_status} />
                  </td>
                  <td>{actorName(r.requested_by, user)}</td>
                  <td className="small text-nowrap">{formatTime(r.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={requests.meta} offset={offset} onChange={setOffset} count={requests.data.length} />
    </>
  )
}
