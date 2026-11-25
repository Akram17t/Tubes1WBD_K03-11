import { Link, useNavigate, useOutletContext, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { PositionBadge } from '../../components/Badges.jsx'
import { actorName, formatTime } from '../../components/labels.js'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'
import { DefinitionCard } from '../workflow/RequestDetail.jsx'

// tombol pengajuan operasi -> operation + execution target yang dikirim ke POST /requests
const REQUESTS = [
  { action: 'request_up', operation: 'up', label: 'Ajukan up', target: () => null },
  { action: 'request_rollback', operation: 'rollback', label: 'Ajukan rollback', target: (m) => m.active_execution_id },
  { action: 'request_forward', operation: 'forward', label: 'Ajukan forward', target: (m) => m.forward_execution_id },
]

// detail definisi + tombol edit dan pengajuan request sesuai allowed_actions dari backend
export function MigrationDetail() {
  const { database } = useOutletContext()
  const { migrationId } = useParams()
  const { user } = useAuth()
  const navigate = useNavigate()
  const base = `/databases/${database.id}`
  const migration = useApi((signal) => api.get(`${base}/migrations/${migrationId}`, null, { signal }), [database.id, migrationId])
  const action = useAction()

  if (migration.loading && !migration.data) return <Loading />
  if (migration.error) return <ErrorAlert error={migration.error} onRetry={migration.reload} />

  const m = migration.data
  const can = (name) => m.allowed_actions.includes(name)

  // bikin request draft lalu langsung buka halamannya buat di-submit
  const createRequest = ({ operation, target }) =>
    action
      .run(() => api.post(`${base}/requests`, { migration_id: m.id, revision: m.revision, operation, target_execution_id: target(m) || null, source_job_id: null }))
      .then((res) => navigate(`${base}/requests/${res.data.id}`))
      .catch(() => {})

  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <Link to={`${base}/migrations`} className="small text-decoration-none">
          ‹ Daftar migration
        </Link>
        <h2 className="h5 mb-0 me-auto">
          Migration #{m.id} · {m.title}
        </h2>
        <PositionBadge position={m.position} />
      </div>
      <ErrorAlert error={action.error} />

      <div className="row g-3">
        <div className="col-lg-8">
          <DefinitionCard title={`Definisi revisi ${m.revision}`} definition={m} />
        </div>
        <div className="col-lg-4">
          <section className="card mb-3">
            <div className="card-body">
              <h3 className="h6">Aksi</h3>
              {m.allowed_actions.length === 0 && <p className="small text-secondary mb-0">Tidak ada aksi yang tersedia untukmu saat ini.</p>}
              <div className="d-flex flex-wrap gap-2">
                {can('edit') && (
                  <Link to={`${base}/migrations/${m.id}/edit`} className="btn btn-outline-primary">
                    Edit
                  </Link>
                )}
                {REQUESTS.filter((r) => can(r.action)).map((r) => (
                  <button key={r.action} type="button" className="btn btn-primary" disabled={action.busy} onClick={() => createRequest(r)}>
                    {r.label}
                  </button>
                ))}
              </div>
              {m.locked_at && (
                <p className="small text-secondary mt-2 mb-0">Definisi terkunci karena sudah dipakai request. SQL tidak bisa diubah lagi.</p>
              )}
            </div>
          </section>
          <section className="card">
            <div className="card-body small">
              <h3 className="h6">Info</h3>
              <dl className="row mb-0">
                <dt className="col-5 fw-normal text-secondary">Pembuat</dt>
                <dd className="col-7">{actorName(m.created_by, user)}</dd>
                <dt className="col-5 fw-normal text-secondary">Revisi</dt>
                <dd className="col-7">{m.revision}</dd>
                <dt className="col-5 fw-normal text-secondary">Dibuat</dt>
                <dd className="col-7">{formatTime(m.created_at)}</dd>
                <dt className="col-5 fw-normal text-secondary">Diubah</dt>
                <dd className="col-7">{formatTime(m.updated_at)}</dd>
                <dt className="col-5 fw-normal text-secondary">Dikunci</dt>
                <dd className="col-7 mb-0">{formatTime(m.locked_at)}</dd>
              </dl>
            </div>
          </section>
        </div>
      </div>
    </>
  )
}
