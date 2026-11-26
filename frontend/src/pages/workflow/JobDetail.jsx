import { useEffect } from 'react'
import { Link, useNavigate, useOutletContext, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { JobStatusBadge, OperationBadge } from '../../components/Badges.jsx'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { actorName, formatTime, isJobActive, progressPercent } from '../../components/labels.js'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi, usePolling } from '../../hooks/useApi.js'

// penjelasan tiap status buat pengguna. outcome_unknown ga disebut gagal & ga nyuruh jalanin SQL manual
const EXPLAIN = {
  queued: 'Job menunggu giliran worker. Halaman ini memperbarui status otomatis.',
  running: 'Worker sedang menjalankan migration. Halaman ini memperbarui status otomatis.',
  success: 'Migration selesai dan hasilnya sudah tercatat di history.',
  failed: 'Migration gagal tanpa perubahan yang tersisa di database. Lihat pesan error di bawah.',
  compensated: 'Perubahan sebagian dari job ini sudah dibatalkan seluruhnya lewat job kompensasi.',
  partial: 'Sebagian batch sudah di-commit lalu job berhenti. Database ditahan sampai job dilanjutkan (resume) atau dikompensasi.',
  partially_reverted: 'Kompensasi baru membatalkan sebagian batch. Database ditahan sampai kompensasi dilanjutkan.',
  outcome_unknown:
    'Server belum bisa memastikan apakah perubahan terakhir sudah di-commit. Sistem akan memeriksa bukti di database target dulu. Jangan menjalankan ulang SQL secara manual.',
}

// status job + progress dari backend, di-poll tiap 2 detik selama job masih antre/jalan
export function JobDetail() {
  const { database, reloadDatabase } = useOutletContext()
  const { jobId } = useParams()
  const { user } = useAuth()
  const navigate = useNavigate()
  const base = `/databases/${database.id}`
  const job = useApi((signal) => api.get(`${base}/jobs/${jobId}`, null, { signal }), [database.id, jobId])
  const action = useAction()
  const active = isJobActive(job.data?.status)
  usePolling(job.reload, active)

  // Segarkan header juga kalau job sudah selesai sebelum halaman ini dibuka.
  useEffect(() => {
    if (job.data && !active) reloadDatabase()
  }, [job.data?.status, active, reloadDatabase])

  if (job.loading && !job.data) return <Loading />
  if (job.error && !job.data) return <ErrorAlert error={job.error} onRetry={job.reload} />

  const j = job.data
  const can = (name) => j.allowed_actions.includes(name)

  // resume = job yang sama masuk antrean lagi (202), ga bawa SQL/config baru
  const resume = () =>
    action.run(async () => {
      await api.post(`${base}/jobs/${j.id}/resume`)
      job.reload()
      reloadDatabase()
    })

  // kompensasi = request baru yang harus di-review dulu, bukan langsung jalan
  const compensate = () =>
    action
      .run(() => api.post(`${base}/requests`, { migration_id: j.migration_id, revision: j.revision, operation: 'compensate', target_execution_id: null, source_job_id: j.id }))
      .then((res) => navigate(`${base}/requests/${res.data.id}`))

  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <Link to={`${base}/requests/${j.request_id}`} className="small text-decoration-none">
          ‹ Request #{j.request_id}
        </Link>
        <h2 className="h5 mb-0 me-auto">Job #{j.id}</h2>
        <OperationBadge operation={j.operation} />
        <JobStatusBadge status={j.status} />
      </div>

      {/* error polling ditampilin tanpa ngilangin data terakhir */}
      <ErrorAlert error={job.error} onRetry={job.reload} />
      <ErrorAlert error={action.error} />

      <div className={`alert alert-${j.status === 'outcome_unknown' ? 'warning' : j.status === 'success' ? 'success' : 'light border'}`}>
        {EXPLAIN[j.status] || j.status}
        {j.blocking_database && <strong className="d-block mt-1">Operasi migration lain di database ini ditahan sampai keadaan ini diselesaikan.</strong>}
      </div>

      <div className="row g-3">
        <div className="col-lg-8">
          <Progress job={j} />
          {j.error && (
            <section className="card mt-3 border-danger">
              <div className="card-body">
                <h3 className="h6">Error terakhir</h3>
                <p className="mb-1">{j.error.message}</p>
                <p className="small text-secondary font-monospace mb-0">
                  {j.error.code}
                  {j.error.sqlstate && ` · SQLSTATE ${j.error.sqlstate}`}
                </p>
              </div>
            </section>
          )}
        </div>
        <div className="col-lg-4">
          {(can('resume') || can('request_compensate')) && (
            <section className="card mb-3 border-primary">
              <div className="card-body">
                <h3 className="h6">Aksi</h3>
                <div className="d-flex flex-wrap gap-2">
                  {can('resume') && (
                    <ConfirmButton busy={action.busy} onConfirm={resume} confirmText="Ya, lanjutkan" className="btn btn-primary">
                      Resume job
                    </ConfirmButton>
                  )}
                  {can('request_compensate') && (
                    <ConfirmButton busy={action.busy} onConfirm={compensate} confirmText="Ya, ajukan" className="btn btn-outline-danger">
                      Ajukan kompensasi
                    </ConfirmButton>
                  )}
                </div>
                <p className="small text-secondary mt-2 mb-0">
                  Resume melanjutkan dari batch terakhir yang ter-commit dengan approval yang sama. Kompensasi membatalkan batch yang sudah
                  ter-commit dan perlu di-review lagi.
                </p>
              </div>
            </section>
          )}
          <section className="card">
            <div className="card-body small">
              <h3 className="h6">Info</h3>
              <dl className="row mb-0">
                <dt className="col-5 fw-normal text-secondary">Migration</dt>
                <dd className="col-7">
                  <Link to={`${base}/migrations/${j.migration_id}`}>#{j.migration_id}</Link> rev {j.revision}
                </dd>
                <dt className="col-5 fw-normal text-secondary">Dijalankan oleh</dt>
                <dd className="col-7">{actorName(j.execution_actor_id, user)}</dd>
                <dt className="col-5 fw-normal text-secondary">Attempt</dt>
                <dd className="col-7">{j.attempt_count}</dd>
                {j.next_attempt_at && j.status === 'queued' && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">Coba lagi</dt>
                    <dd className="col-7">{formatTime(j.next_attempt_at)}</dd>
                  </>
                )}
                {j.compensation_job_id && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">Job kompensasi</dt>
                    <dd className="col-7">
                      <Link to={`${base}/jobs/${j.compensation_job_id}`}>#{j.compensation_job_id}</Link>
                    </dd>
                  </>
                )}
                <dt className="col-5 fw-normal text-secondary">Masuk antrean</dt>
                <dd className="col-7">{formatTime(j.queued_at)}</dd>
                <dt className="col-5 fw-normal text-secondary">Selesai</dt>
                <dd className="col-7 mb-0">{formatTime(j.finished_at)}</dd>
              </dl>
              <Link to={`${base}/history`} className="d-inline-block mt-2">
                Lihat history eksekusi ›
              </Link>
            </div>
          </section>
        </div>
      </div>
    </>
  )
}

// progress dari angka yang diakui commit di backend, bukan timer frontend
function Progress({ job }) {
  const p = job.progress
  const percent = progressPercent(p)
  return (
    <section className="card">
      <div className="card-body">
        <h3 className="h6">Progress</h3>
        {percent !== null && (
          <div className="progress mb-2" role="progressbar" aria-label="Progress job" aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}>
            <div className={'progress-bar' + (isJobActive(job.status) ? ' progress-bar-striped progress-bar-animated' : '')} style={{ width: `${percent}%` }}>
              {percent}%
            </div>
          </div>
        )}
        <dl className="row small mb-0">
          <dt className="col-6 col-md-4 fw-normal text-secondary">Row ter-commit</dt>
          <dd className="col-6 col-md-8">
            {p.committed_rows}
            {p.total_rows !== null && ` dari ${p.total_rows}`}
          </dd>
          <dt className="col-6 col-md-4 fw-normal text-secondary">Batch ter-commit</dt>
          <dd className="col-6 col-md-8">{p.committed_batches}</dd>
          {(p.reverted_rows > 0 || p.reverted_batches > 0) && (
            <>
              <dt className="col-6 col-md-4 fw-normal text-secondary">Dibatalkan</dt>
              <dd className="col-6 col-md-8">
                {p.reverted_rows} row, {p.reverted_batches} batch
              </dd>
            </>
          )}
          <dt className="col-6 col-md-4 fw-normal text-secondary">Cursor terakhir</dt>
          <dd className="col-6 col-md-8 font-monospace mb-0">{p.last_cursor ?? '—'}</dd>
        </dl>
      </div>
    </section>
  )
}
