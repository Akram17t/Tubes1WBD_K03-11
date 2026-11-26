import { Link, useOutletContext, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { OperationBadge, ReviewStatusBadge, SqlBlock } from '../../components/Badges.jsx'
import { ConfirmButton } from '../../components/ConfirmButton.jsx'
import { actorName, formatTime } from '../../components/labels.js'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

// SQL mana yang beneran dijalanin worker buat tiap operasi
const RUNS = { up: 'sql_up', forward: 'sql_up', rollback: 'sql_down', compensate: 'sql_down' }

// detail request: snapshot yang dibaca leader, slot approval per group, dan aksi sesuai allowed_actions.
// Tombol eksekusi sengaja dipisah dari approval (kontrak: eksekusi nunggu pengguna menekan tombol)
export function RequestDetail() {
  const { database, reloadDatabase } = useOutletContext()
  const { requestId } = useParams()
  const { user } = useAuth()
  const request = useApi((signal) => api.get(`/databases/${database.id}/requests/${requestId}`, null, { signal }), [database.id, requestId])
  const action = useAction()

  if (request.loading && !request.data) return <Loading />
  if (request.error) return <ErrorAlert error={request.error} onRetry={request.reload} />

  const r = request.data
  const can = (name) => r.allowed_actions.includes(name)
  const base = `/databases/${database.id}/requests/${r.id}`

  // jalanin aksi lalu muat ulang detail; execute juga nyegerin status database (job aktif).
  // Kalau execute gagal tanpa jawaban jelas (jaringan/5xx), ga diulang otomatis: cukup muat ulang
  // detail biar kelihatan job-nya sudah kebentuk atau belum
  const run = (name) =>
    action.run(async () => {
      try {
        await api.post(`${base}/${name}`)
      } catch (err) {
        if (name === 'execute' && (!err.status || err.status >= 500)) {
          err.message = 'Hasil eksekusi belum diketahui. Halaman dimuat ulang untuk mengecek apakah job sudah dibuat.'
          request.reload()
        }
        throw err
      }
      request.reload()
      if (name === 'execute') reloadDatabase()
    })

  const pendingNames = action.error?.code === 'approval_incomplete' ? pendingGroupNames(r, action.error.details?.pending_group_ids) : null
  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <Link to={`/databases/${database.id}/requests`} className="small text-decoration-none">
          ‹ Daftar request
        </Link>
        <h2 className="h5 mb-0 me-auto">
          Request #{r.id} · {r.snapshot.title}
        </h2>
        <OperationBadge operation={r.operation} />
        <ReviewStatusBadge status={r.review_status} />
      </div>

      <ErrorAlert error={action.error} />
      {pendingNames && <div className="alert alert-warning">Masih menunggu approval dari: {pendingNames}.</div>}

      <div className="row g-3">
        <div className="col-lg-8">
          <DefinitionCard title="Snapshot definisi (yang dibaca leader)" definition={r.snapshot} runs={RUNS[r.operation]} />
        </div>
        <div className="col-lg-4">
          <section className="card mb-3">
            <div className="card-body">
              <h3 className="h6">Aksi</h3>
              {r.allowed_actions.length === 0 && <p className="small text-secondary mb-0">Tidak ada aksi yang tersedia untukmu saat ini.</p>}
              <div className="d-flex flex-wrap gap-2">
                {can('submit') && (
                  <button type="button" className="btn btn-primary" disabled={action.busy} onClick={() => run('submit').catch(() => {})}>
                    Submit untuk review
                  </button>
                )}
                {can('approve') && (
                  <button type="button" className="btn btn-success" disabled={action.busy} onClick={() => run('approve').catch(() => {})}>
                    Setujui
                  </button>
                )}
                {can('cancel') && (
                  <ConfirmButton busy={action.busy} onConfirm={() => run('cancel')} confirmText="Batalkan request" className="btn btn-outline-danger">
                    Batalkan
                  </ConfirmButton>
                )}
              </div>
            </div>
          </section>

          {(can('execute') || r.job_id) && (
            <section className="card mb-3 border-primary">
              <div className="card-body">
                <h3 className="h6">Eksekusi</h3>
                {r.job_id ? (
                  <>
                    <p className="small mb-2">Request ini sudah masuk antrean sebagai job.</p>
                    <Link to={`/databases/${database.id}/jobs/${r.job_id}`} className="btn btn-outline-primary">
                      Lihat job #{r.job_id}
                    </Link>
                  </>
                ) : (
                  <>
                    <p className="small text-secondary">
                      Semua approval sudah lengkap. Menekan tombol ini memasukkan migration ke antrean; worker menjalankannya di latar belakang.
                    </p>
                    <ConfirmButton
                      busy={action.busy}
                      onConfirm={() => run('execute')}
                      confirmText="Ya, jalankan"
                      className="btn btn-primary"
                    >
                      Jalankan migration
                    </ConfirmButton>
                  </>
                )}
              </div>
            </section>
          )}

          <section className="card mb-3">
            <div className="card-body small">
              <h3 className="h6">Info</h3>
              <dl className="row mb-0">
                <dt className="col-5 fw-normal text-secondary">Pengaju</dt>
                <dd className="col-7">{actorName(r.requested_by, user)}</dd>
                <dt className="col-5 fw-normal text-secondary">Revisi</dt>
                <dd className="col-7">{r.revision}</dd>
                {r.target_execution_id && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">Target eksekusi</dt>
                    <dd className="col-7 font-monospace">#{r.target_execution_id}</dd>
                  </>
                )}
                {r.source_job_id && (
                  <>
                    <dt className="col-5 fw-normal text-secondary">Job sumber</dt>
                    <dd className="col-7 font-monospace">#{r.source_job_id}</dd>
                  </>
                )}
                <dt className="col-5 fw-normal text-secondary">Dibuat</dt>
                <dd className="col-7">{formatTime(r.created_at)}</dd>
                <dt className="col-5 fw-normal text-secondary">Di-submit</dt>
                <dd className="col-7 mb-0">{formatTime(r.submitted_at)}</dd>
              </dl>
            </div>
          </section>
        </div>
      </div>

      <Approvals request={r} me={user} />
    </>
  )
}

// isi definisi migration: SQL, mode batch, irreversible. `runs` = SQL yang bakal dijalanin (ditandai)
export function DefinitionCard({ title, definition: s, runs }) {
  return (
    <section className="card">
      <div className="card-body">
        <h3 className="h6">{title}</h3>
        {s.description && <p className="mb-2">{s.description}</p>}
        <p className="small mb-3">
          Mode <strong>{s.mode === 'batch' ? 'batch' : 'normal'}</strong>
          {s.irreversible && <span className="badge text-bg-danger ms-2">Irreversible</span>}
        </p>
        {s.irreversible && (
          <div className="alert alert-danger py-2 small">
            Migration ini tidak bisa di-rollback. Alasan: {(s.irreversible_reason || 'tidak dicantumkan').replace(/\.$/, '')}.
          </div>
        )}
        {s.mode === 'batch' && s.batch_config && (
          <>
            <h4 className="h6 small text-secondary">Konfigurasi batch</h4>
            <SqlBlock sql={JSON.stringify(s.batch_config, null, 2)} label="Konfigurasi batch" />
          </>
        )}
        <h4 className="h6 small text-secondary mt-3">
          SQL up {runs === 'sql_up' && <span className="badge text-bg-primary ms-1">dijalankan</span>}
        </h4>
        <SqlBlock sql={s.sql_up} label="SQL up" />
        <h4 className="h6 small text-secondary mt-3">
          SQL down {runs === 'sql_down' && <span className="badge text-bg-primary ms-1">dijalankan</span>}
        </h4>
        <SqlBlock sql={s.sql_down} label="SQL down" />
      </div>
    </section>
  )
}

// slot approval per group; group tanpa leader dijelasin terang-terangan
function Approvals({ request, me }) {
  if (request.review_status === 'draft') {
    return <p className="small text-secondary mt-3">Daftar group yang wajib menyetujui ditentukan saat request di-submit.</p>
  }
  if (request.approvals.length === 0) return null
  return (
    <section className="card mt-3">
      <div className="card-body">
        <h3 className="h6">Approval per group</h3>
        <div className="table-responsive">
          <table className="table align-middle mb-0 small">
            <thead>
              <tr>
                <th>Group</th>
                <th>Status</th>
                <th>Oleh</th>
                <th>Waktu</th>
              </tr>
            </thead>
            <tbody>
              {request.approvals.map((a) => (
                <tr key={a.group_id}>
                  <td className="fw-semibold">{a.group_name}</td>
                  <td>
                    {a.approved_by ? (
                      <span className="badge text-bg-success">Disetujui</span>
                    ) : a.waiting_for_leader ? (
                      <span className="badge text-bg-warning">Belum ada leader</span>
                    ) : (
                      <span className="badge text-bg-secondary">Menunggu leader</span>
                    )}
                    {a.can_approve && <span className="badge text-bg-info ms-1">Kamu bisa menyetujui</span>}
                  </td>
                  <td>{actorName(a.approved_by, me)}</td>
                  <td className="text-nowrap">{formatTime(a.approved_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {request.approvals.some((a) => a.waiting_for_leader && !a.approved_by) && (
          <p className="small text-secondary mt-2 mb-0">
            Group tanpa leader aktif membuat request tetap menunggu. Minta Platform Admin menunjuk leader untuk group tersebut.
          </p>
        )}
      </div>
    </section>
  )
}

// ubah pending_group_ids dari error 409 jadi nama group yang bisa dibaca
export function pendingGroupNames(request, ids = []) {
  const names = ids.map((id) => request.approvals.find((a) => a.group_id === id)?.group_name || `Group #${id}`)
  return names.join(', ')
}
