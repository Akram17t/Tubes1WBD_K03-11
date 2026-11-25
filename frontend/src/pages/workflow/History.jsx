import { Link, useOutletContext, useSearchParams } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { JobStatusBadge, OperationBadge } from '../../components/Badges.jsx'
import { actorName, formatTime } from '../../components/labels.js'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// timeline semua attempt (termasuk yang gagal), urut naik dari server.
// Hide rollback disimpen di URL (?hide_rollback=1) biar ikut kebawa waktu refresh
export function History() {
  const { database } = useOutletContext()
  const { user } = useAuth()
  const [params, setParams] = useSearchParams()
  const [offset, setOffset] = useOffset()
  const hide = params.get('hide_rollback') === '1'
  const base = `/databases/${database.id}`
  const history = useApi(
    (signal) => api.get(`${base}/history`, { hide_rollback: hide, limit: PAGE_LIMIT, offset }, { signal }),
    [database.id, hide, offset],
  )

  // ganti filter = balik ke halaman pertama, karena pagination dihitung setelah filter
  const toggleHide = (e) => {
    const copy = new URLSearchParams(params)
    if (e.target.checked) copy.set('hide_rollback', '1')
    else copy.delete('hide_rollback')
    copy.delete('offset')
    setParams(copy)
  }

  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <p className="small text-secondary mb-0 me-auto">Setiap percobaan eksekusi tercatat, termasuk yang gagal dan retry.</p>
        <div className="form-check form-switch mb-0">
          <input id="hide_rollback" type="checkbox" className="form-check-input" checked={hide} onChange={toggleHide} />
          <label htmlFor="hide_rollback" className="form-check-label">
            Hide rollback
          </label>
        </div>
      </div>
      {hide && (
        <p className="small text-secondary">
          Menyembunyikan eksekusi sukses yang sudah di-rollback beserta rollback sukses yang membatalkannya. Kegagalan tetap ditampilkan.
        </p>
      )}

      {history.loading && !history.data ? (
        <Loading />
      ) : history.error ? (
        <ErrorAlert error={history.error} onRetry={history.reload} />
      ) : history.data.length === 0 ? (
        <Empty text="Belum ada eksekusi migration di database ini." />
      ) : (
        <div className="table-responsive">
          <table className="table table-hover align-middle bg-white small">
            <thead>
              <tr>
                <th>Eksekusi</th>
                <th>Migration</th>
                <th>Operasi</th>
                <th>Status</th>
                <th>Oleh</th>
                <th>Mulai</th>
                <th>Selesai</th>
              </tr>
            </thead>
            <tbody>
              {history.data.map((e) => (
                <tr key={e.id}>
                  <td className="font-monospace text-nowrap">
                    <Link to={`${base}/executions/${e.id}`}>#{e.id}</Link>
                    <span className="text-secondary"> · attempt {e.attempt}</span>
                  </td>
                  <td>
                    {e.migration_title || `Migration #${e.migration_id}`}
                    <span className="text-secondary ms-1">rev {e.revision}</span>
                  </td>
                  <td className="text-nowrap">
                    <OperationBadge operation={e.operation} />
                    {e.target_execution_id && (
                      <Link to={`${base}/executions/${e.target_execution_id}`} className="ms-1">
                        → #{e.target_execution_id}
                      </Link>
                    )}
                  </td>
                  <td>
                    <JobStatusBadge status={e.status} />
                  </td>
                  <td>{actorName(e.actor_id, user)}</td>
                  <td className="text-nowrap">{formatTime(e.started_at)}</td>
                  <td className="text-nowrap">{formatTime(e.finished_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={history.meta} offset={offset} onChange={setOffset} count={history.data?.length} />
    </>
  )
}
