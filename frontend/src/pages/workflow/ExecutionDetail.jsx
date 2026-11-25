import { Link, useOutletContext, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { JobStatusBadge, OperationBadge, SqlBlock } from '../../components/Badges.jsx'
import { actorName, formatTime } from '../../components/labels.js'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// detail satu attempt: SQL snapshot yang beneran dijalanin, error, relasi rollback/forward, dan ledger batch
export function ExecutionDetail() {
  const { database } = useOutletContext()
  const { executionId } = useParams()
  const { user } = useAuth()
  const base = `/databases/${database.id}`
  const execution = useApi((signal) => api.get(`${base}/executions/${executionId}`, null, { signal }), [database.id, executionId])

  if (execution.loading && !execution.data) return <Loading />
  if (execution.error) return <ErrorAlert error={execution.error} onRetry={execution.reload} />

  const e = execution.data
  const batches = e.batch_attempts || []
  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <Link to={`${base}/history`} className="small text-decoration-none">
          ‹ History
        </Link>
        <h2 className="h5 mb-0 me-auto">
          Eksekusi #{e.id} · {e.migration_title || `Migration #${e.migration_id}`}
        </h2>
        <OperationBadge operation={e.operation} />
        <JobStatusBadge status={e.status} />
      </div>
      {e.status === 'outcome_unknown' && (
        <div className="alert alert-warning">Hasil attempt ini belum bisa dipastikan. Sistem masih memeriksa bukti di database target.</div>
      )}

      <div className="row g-3">
        <div className="col-lg-8">
          <section className="card">
            <div className="card-body">
              <h3 className="h6">SQL yang dijalankan</h3>
              <SqlBlock sql={e.sql_snapshot} label="SQL snapshot" />
              {e.error_message && (
                <div className="alert alert-danger small mt-3 mb-0">
                  {e.error_message}
                  {e.sqlstate && <span className="d-block font-monospace text-secondary mt-1">SQLSTATE {e.sqlstate}</span>}
                </div>
              )}
            </div>
          </section>
          {batches.length > 0 && <Batches batches={batches} />}
        </div>
        <div className="col-lg-4">
          <section className="card">
            <div className="card-body small">
              <h3 className="h6">Info</h3>
              <dl className="row mb-0">
                <dt className="col-5 fw-normal text-secondary">Migration</dt>
                <dd className="col-7">
                  <Link to={`${base}/migrations/${e.migration_id}`}>#{e.migration_id}</Link> rev {e.revision}
                </dd>
                <dt className="col-5 fw-normal text-secondary">Job</dt>
                <dd className="col-7">{e.job_id ? <Link to={`${base}/jobs/${e.job_id}`}>#{e.job_id}</Link> : '—'}</dd>
                <dt className="col-5 fw-normal text-secondary">Attempt</dt>
                <dd className="col-7">{e.attempt}</dd>
                {e.target_execution_id && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">{e.operation === 'forward' ? 'Forward dari' : 'Membatalkan'}</dt>
                    <dd className="col-7">
                      <Link to={`${base}/executions/${e.target_execution_id}`}>Eksekusi #{e.target_execution_id}</Link>
                    </dd>
                  </>
                )}
                <dt className="col-5 fw-normal text-secondary">Oleh</dt>
                <dd className="col-7">{actorName(e.executed_by, user)}</dd>
                {e.progress?.committed_rows > 0 && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">Row ter-commit</dt>
                    <dd className="col-7">{e.progress.committed_rows}</dd>
                  </>
                )}
                <dt className="col-5 fw-normal text-secondary">Mulai</dt>
                <dd className="col-7">{formatTime(e.started_at)}</dd>
                <dt className="col-5 fw-normal text-secondary">Selesai</dt>
                <dd className="col-7 mb-0">{formatTime(e.finished_at)}</dd>
              </dl>
            </div>
          </section>
        </div>
      </div>
    </>
  )
}

// ledger per batch: cakupan ID, row count, hasil. SQL aktual tiap batch bisa dibuka
function Batches({ batches }) {
  return (
    <section className="card mt-3">
      <div className="card-body">
        <h3 className="h6">Batch</h3>
        <div className="table-responsive">
          <table className="table align-middle small mb-0">
            <thead>
              <tr>
                <th>#</th>
                <th>Arah</th>
                <th>Cakupan ID</th>
                <th>Row</th>
                <th>Status</th>
                <th>SQL</th>
              </tr>
            </thead>
            <tbody>
              {batches.map((b) => (
                <tr key={b.id}>
                  <td>{b.batch_number}</td>
                  <td className="text-uppercase">{b.direction}</td>
                  <td className="font-monospace text-nowrap">{b.first_id !== null ? `${b.first_id}–${b.last_id}` : '—'}</td>
                  <td>{b.row_count}</td>
                  <td>
                    <JobStatusBadge status={b.status} />
                    {b.error_message && <span className="d-block text-danger">{b.error_message}</span>}
                  </td>
                  <td>
                    <details>
                      <summary>Lihat</summary>
                      <SqlBlock sql={b.sql_snapshot} label={`SQL batch ${b.batch_number}`} />
                    </details>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
