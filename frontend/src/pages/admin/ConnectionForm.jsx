import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { PageHeader } from '../../components/PageHeader.jsx'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'
import { TestConnectionButton } from './TestConnectionButton.jsx'

const SSL_MODES = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full']
const EMPTY = { name: '', host: '', port: 5432, database_name: '', db_username: '', ssl_mode: 'require', password: '', is_active: true }
const FIELDS = ['name', 'host', 'port', 'database_name', 'db_username', 'ssl_mode', 'is_active']

// form tambah/edit koneksi. Password write-only: waktu edit, kosong = password lama dipakai
export function ConnectionForm() {
  const { connectionId } = useParams()
  const editing = Boolean(connectionId)
  const navigate = useNavigate()
  const current = useApi(
    (signal) => (editing ? api.get(`/admin/connections/${connectionId}`, null, { signal }) : Promise.resolve({ data: null })),
    [connectionId],
  )
  const [form, setForm] = useState(EMPTY)
  const action = useAction()

  useEffect(() => {
    if (current.data) setForm({ ...EMPTY, ...current.data, password: '' })
  }, [current.data])

  if (editing && current.loading) return <Loading />
  if (editing && current.error) return <ErrorAlert error={current.error} onRetry={current.reload} />

  const set = (field) => (e) => {
    const { type, checked, value } = e.target
    setForm((f) => ({ ...f, [field]: type === 'checkbox' ? checked : type === 'number' ? Number(value) : value }))
  }

  // edit: kirim field yang berubah aja; create: kirim semua
  function buildBody() {
    if (!editing) {
      const { is_active: _ignored, ...body } = form
      return body
    }
    const body = {}
    for (const field of FIELDS) if (form[field] !== current.data[field]) body[field] = form[field]
    if (form.password) body.password = form.password
    return body
  }

  async function handleSubmit(event) {
    event.preventDefault()
    try {
      await action.run(() => (editing ? api.patch(`/admin/connections/${connectionId}`, buildBody()) : api.post('/admin/connections', buildBody())))
      navigate('/admin/connections')
    } catch {
      /* error tampil lewat action.error */
    }
  }

  return (
    <>
      <PageHeader title={editing ? `Edit koneksi ${current.data?.name}` : 'Tambah koneksi'} back={{ to: '/admin/connections', label: 'Daftar koneksi' }}>
        {editing && <TestConnectionButton connectionId={connectionId} disabled={!current.data?.is_active} />}
      </PageHeader>
      <div className="card">
        <form className="card-body" onSubmit={handleSubmit}>
          <ErrorAlert error={action.error} />
          <div className="row g-3">
            <Field id="name" label="Nama koneksi" className="col-md-6">
              <input id="name" className="form-control" required maxLength={100} value={form.name} onChange={set('name')} />
            </Field>
            <Field id="ssl_mode" label="SSL mode" className="col-md-6">
              <select id="ssl_mode" className="form-select" value={form.ssl_mode} onChange={set('ssl_mode')}>
                {SSL_MODES.map((mode) => (
                  <option key={mode}>{mode}</option>
                ))}
              </select>
            </Field>
            <Field id="host" label="Host" className="col-md-8">
              <input id="host" className="form-control" required value={form.host} onChange={set('host')} />
            </Field>
            <Field id="port" label="Port" className="col-md-4">
              <input id="port" type="number" min={1} max={65535} className="form-control" required value={form.port} onChange={set('port')} />
            </Field>
            <Field id="database_name" label="Nama database" className="col-md-6">
              <input id="database_name" className="form-control" required value={form.database_name} onChange={set('database_name')} />
            </Field>
            <Field id="db_username" label="Username database" className="col-md-6">
              <input id="db_username" className="form-control" required autoComplete="off" value={form.db_username} onChange={set('db_username')} />
            </Field>
            <Field
              id="password"
              label="Password database"
              className="col-md-6"
              help={editing ? 'Kosongkan untuk tetap memakai password lama. Password tersimpan terenkripsi dan tidak pernah ditampilkan.' : 'Disimpan terenkripsi di server.'}
            >
              <input
                id="password"
                type="password"
                className="form-control"
                autoComplete="new-password"
                required={!editing}
                value={form.password}
                onChange={set('password')}
              />
            </Field>
            {editing && (
              <div className="col-md-6 d-flex align-items-center">
                <div className="form-check mt-md-4">
                  <input id="is_active" type="checkbox" className="form-check-input" checked={form.is_active} onChange={set('is_active')} />
                  <label htmlFor="is_active" className="form-check-label">
                    Koneksi aktif
                  </label>
                  <div className="form-text">Menonaktifkan koneksi mencabut session user yang punya akses ke database ini.</div>
                </div>
              </div>
            )}
          </div>
          <div className="mt-4 d-flex gap-2">
            <button type="submit" className="btn btn-primary" disabled={action.busy}>
              {action.busy ? 'Menyimpan...' : 'Simpan'}
            </button>
            <button type="button" className="btn btn-outline-secondary" onClick={() => navigate('/admin/connections')}>
              Batal
            </button>
          </div>
        </form>
      </div>
    </>
  )
}

function Field({ id, label, help, className, children }) {
  return (
    <div className={className}>
      <label htmlFor={id} className="form-label">
        {label}
      </label>
      {children}
      {help && <div className="form-text">{help}</div>}
    </div>
  )
}
