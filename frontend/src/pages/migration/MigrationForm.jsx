import { useEffect, useState } from 'react'
import { useNavigate, useOutletContext, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

const EMPTY_BATCH = { schema: 'public', table: '', key_column: 'id', batch_size: 1000 }
const EMPTY = { title: '', description: '', sql_up: '', sql_down: '', irreversible: false, irreversible_reason: '', mode: 'normal', batch_config: EMPTY_BATCH }

// form buat/edit definisi. Edit bawa revision yang diharapkan; kalau sudah berubah di server -> 409
export function MigrationForm() {
  const { database } = useOutletContext()
  const { migrationId } = useParams()
  const editing = Boolean(migrationId)
  const navigate = useNavigate()
  const base = `/databases/${database.id}/migrations`
  const current = useApi(
    (signal) => (editing ? api.get(`${base}/${migrationId}`, null, { signal }) : Promise.resolve({ data: null })),
    [migrationId],
  )
  const [form, setForm] = useState(EMPTY)
  const action = useAction()

  useEffect(() => {
    const m = current.data
    if (m) setForm({ ...EMPTY, ...m, description: m.description || '', sql_down: m.sql_down || '', irreversible_reason: m.irreversible_reason || '', batch_config: m.batch_config || EMPTY_BATCH })
  }, [current.data])

  if (editing && current.loading) return <Loading />
  if (editing && current.error) return <ErrorAlert error={current.error} onRetry={current.reload} />

  const set = (field) => (e) => setForm((f) => ({ ...f, [field]: e.target.type === 'checkbox' ? e.target.checked : e.target.value }))
  const setBatch = (field) => (e) =>
    setForm((f) => ({ ...f, batch_config: { ...f.batch_config, [field]: field === 'batch_size' ? Number(e.target.value) : e.target.value } }))

  // string kosong dikirim null biar sesuai kontrak (nullable = null)
  function buildBody() {
    const body = {
      title: form.title.trim(),
      description: form.description.trim() || null,
      sql_up: form.sql_up,
      sql_down: form.sql_down || null,
      irreversible: form.irreversible,
      irreversible_reason: form.irreversible ? form.irreversible_reason.trim() : null,
      mode: form.mode,
      batch_config: form.mode === 'batch' ? form.batch_config : null,
    }
    return editing ? { ...body, revision: current.data.revision } : body
  }

  async function handleSubmit(event) {
    event.preventDefault()
    try {
      const res = await action.run(() => (editing ? api.patch(`${base}/${migrationId}`, buildBody()) : api.post(base, buildBody())))
      navigate(`${base}/${res.data.id}`)
    } catch {
      /* error tampil lewat action.error */
    }
  }

  const invalidField = action.error?.details?.field
  const cls = (field, base = 'form-control') => base + (invalidField === field ? ' is-invalid' : '')
  return (
    <div className="card">
      <form className="card-body" onSubmit={handleSubmit}>
        <h2 className="h5">{editing ? `Edit migration #${migrationId}` : 'Buat migration'}</h2>
        {action.error?.code === 'state_conflict' && editing && (
          <div className="alert alert-warning">Definisi ini sudah diubah orang lain. Muat ulang halaman untuk melihat versi terbaru.</div>
        )}
        <ErrorAlert error={action.error} />
        <div className="row g-3">
          <div className="col-md-8">
            <label htmlFor="title" className="form-label">
              Judul
            </label>
            <input id="title" className={cls('title')} required maxLength={200} value={form.title} onChange={set('title')} />
          </div>
          <div className="col-md-4">
            <label htmlFor="mode" className="form-label">
              Mode
            </label>
            <select id="mode" className="form-select" value={form.mode} onChange={set('mode')}>
              <option value="normal">normal (satu transaction)</option>
              <option value="batch">batch (per kelompok ID)</option>
            </select>
          </div>
          <div className="col-12">
            <label htmlFor="description" className="form-label">
              Deskripsi <span className="text-secondary small">(opsional)</span>
            </label>
            <textarea id="description" className="form-control" rows={2} value={form.description} onChange={set('description')} />
          </div>

          {form.mode === 'batch' && (
            <fieldset className="col-12 border rounded p-3">
              <legend className="float-none w-auto px-1 fs-6 mb-0">Konfigurasi batch</legend>
              <p className="form-text mt-0">
                SQL memakai <code>$1</code> sebagai array ID batch, contoh <code>WHERE id = ANY($1::bigint[])</code>.
              </p>
              <div className="row g-2">
                {[
                  ['schema', 'Schema'],
                  ['table', 'Tabel'],
                  ['key_column', 'Kolom key'],
                ].map(([field, label]) => (
                  <div className="col-6 col-md-3" key={field}>
                    <label htmlFor={`batch_${field}`} className="form-label small">
                      {label}
                    </label>
                    <input id={`batch_${field}`} className={cls(`batch_config.${field}`, 'form-control form-control-sm')} required value={form.batch_config[field]} onChange={setBatch(field)} />
                  </div>
                ))}
                <div className="col-6 col-md-3">
                  <label htmlFor="batch_size" className="form-label small">
                    Ukuran batch
                  </label>
                  <input id="batch_size" type="number" min={1} className="form-control form-control-sm" required value={form.batch_config.batch_size} onChange={setBatch('batch_size')} />
                </div>
              </div>
            </fieldset>
          )}

          <div className="col-12">
            <label htmlFor="sql_up" className="form-label">
              SQL up
            </label>
            <textarea id="sql_up" className={cls('sql_up', 'form-control font-monospace')} rows={6} required spellCheck={false} value={form.sql_up} onChange={set('sql_up')} />
          </div>
          <div className="col-12">
            <div className="form-check">
              <input id="irreversible" type="checkbox" className="form-check-input" checked={form.irreversible} onChange={set('irreversible')} />
              <label htmlFor="irreversible" className="form-check-label">
                Irreversible (tidak bisa di-rollback)
              </label>
            </div>
          </div>
          {form.irreversible && (
            <div className="col-12">
              <label htmlFor="irreversible_reason" className="form-label">
                Alasan irreversible
              </label>
              <input id="irreversible_reason" className={cls('irreversible_reason')} required value={form.irreversible_reason} onChange={set('irreversible_reason')} />
            </div>
          )}
          <div className="col-12">
            <label htmlFor="sql_down" className="form-label">
              SQL down {form.irreversible && <span className="text-secondary small">(opsional, tidak akan dijalankan)</span>}
            </label>
            <textarea
              id="sql_down"
              className={cls('sql_down', 'form-control font-monospace')}
              rows={6}
              required={!form.irreversible}
              spellCheck={false}
              value={form.sql_down}
              onChange={set('sql_down')}
            />
          </div>
        </div>
        <div className="mt-4 d-flex gap-2">
          <button type="submit" className="btn btn-primary" disabled={action.busy}>
            {action.busy ? 'Menyimpan...' : 'Simpan draft'}
          </button>
          <button type="button" className="btn btn-outline-secondary" onClick={() => navigate(editing ? `${base}/${migrationId}` : base)}>
            Batal
          </button>
        </div>
      </form>
    </div>
  )
}
