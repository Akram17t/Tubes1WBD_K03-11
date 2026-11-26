import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { useAuth } from '../../auth/AuthContext.jsx'
import { PageHeader } from '../../components/PageHeader.jsx'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useAction, useApi } from '../../hooks/useApi.js'

const EMPTY = { username: '', display_name: '', password: '', is_platform_admin: false }

// form tambah & edit user. Edit cuma ngirim field yang berubah (PATCH parsial),
// password kosong artinya ga diganti
export function UserForm() {
  const { userId } = useParams()
  const editing = Boolean(userId)
  const { user: me } = useAuth()
  const navigate = useNavigate()
  const current = useApi((signal) => (editing ? api.get(`/admin/users/${userId}`, null, { signal }) : Promise.resolve({ data: null })), [userId])
  const [form, setForm] = useState(EMPTY)
  const action = useAction()

  useEffect(() => {
    if (current.data) setForm({ ...EMPTY, ...current.data, password: '' })
  }, [current.data])

  if (editing && current.loading) return <Loading />
  if (editing && current.error) return <ErrorAlert error={current.error} onRetry={current.reload} />

  const self = editing && userId === me.id
  const set = (field) => (e) => setForm((f) => ({ ...f, [field]: e.target.type === 'checkbox' ? e.target.checked : e.target.value }))

  async function handleSubmit(event) {
    event.preventDefault()
    const patch = editing ? changedFields(current.data, form) : null
    if (editing && Object.keys(patch).length === 0) return navigate('/admin/users')
    try {
      await action.run(() => (editing ? api.patch(`/admin/users/${userId}`, patch) : api.post('/admin/users', form)))
      navigate('/admin/users')
    } catch {
      /* error tampil lewat action.error */
    }
  }

  const usernameTaken = action.error?.details?.field === 'username'
  return (
    <>
      <PageHeader title={editing ? `Edit user ${current.data?.username}` : 'Tambah user'} back={{ to: '/admin/users', label: 'Daftar user' }} />
      <div className="card">
        <form className="card-body" onSubmit={handleSubmit}>
          <ErrorAlert error={usernameTaken ? null : action.error} />
          <div className="row g-3">
            <div className="col-md-6">
              <label htmlFor="username" className="form-label">
                Username
              </label>
              <input
                id="username"
                className={'form-control' + (usernameTaken ? ' is-invalid' : '')}
                required
                autoComplete="off"
                value={form.username}
                onChange={set('username')}
              />
              {usernameTaken && <div className="invalid-feedback">{action.error.message}</div>}
            </div>
            <div className="col-md-6">
              <label htmlFor="display_name" className="form-label">
                Nama tampilan
              </label>
              <input id="display_name" className="form-control" value={form.display_name} onChange={set('display_name')} />
              <div className="form-text">Kosongkan untuk memakai username.</div>
            </div>
            <div className="col-md-6">
              <label htmlFor="password" className="form-label">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="form-control"
                autoComplete="new-password"
                required={!editing}
                value={form.password}
                onChange={set('password')}
              />
              <div className="form-text">
                {editing ? 'Kosongkan jika tidak diganti. Mengganti password mencabut semua session user ini.' : 'Minimal 8 karakter.'}
              </div>
            </div>
            <div className="col-md-6 d-flex align-items-center">
              <div className="form-check mt-md-4">
                <input
                  id="is_platform_admin"
                  type="checkbox"
                  className="form-check-input"
                  checked={form.is_platform_admin}
                  disabled={self}
                  onChange={set('is_platform_admin')}
                />
                <label htmlFor="is_platform_admin" className="form-check-label">
                  Platform Admin
                </label>
                {self && <div className="form-text">Kamu tidak bisa mencabut status admin milikmu sendiri.</div>}
              </div>
            </div>
          </div>
          <div className="mt-4 d-flex gap-2">
            <button type="submit" className="btn btn-primary" disabled={action.busy}>
              {action.busy ? 'Menyimpan...' : 'Simpan'}
            </button>
            <button type="button" className="btn btn-outline-secondary" onClick={() => navigate('/admin/users')}>
              Batal
            </button>
          </div>
        </form>
      </div>
    </>
  )
}

// bandingin dengan data awal, cuma field yang beda yang dikirim
export function changedFields(original, form) {
  const patch = {}
  for (const field of ['username', 'display_name', 'is_platform_admin']) {
    if (form[field] !== original[field]) patch[field] = form[field]
  }
  if (form.password) patch.password = form.password
  return patch
}
