import { useState } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router'
import { useAuth } from '../auth/AuthContext.jsx'
import { safeNext } from '../auth/safeNext.js'
import { ErrorAlert, Loading } from '../components/StateView.jsx'

export function Login() {
  const { status, expired, login } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const [form, setForm] = useState({ username: '', password: '' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  if (status === 'loading') return <Loading text="Memeriksa sesi..." />
  if (status === 'authenticated' && !busy) return <Navigate to={next} replace />

  // submit login, password dikosongin lagi kalau gagal
  async function handleSubmit(event) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(form.username, form.password)
      navigate(next, { replace: true })
    } catch (err) {
      setError(err)
      setForm((f) => ({ ...f, password: '' }))
      setBusy(false)
    }
  }

  const update = (field) => (e) => setForm((f) => ({ ...f, [field]: e.target.value }))

  return (
    <div className="container py-5">
      <div className="row justify-content-center">
        <div className="col-12 col-sm-9 col-md-6 col-lg-4">
          <div className="card shadow-sm">
            <div className="card-body p-4">
              <h1 className="h4 mb-4 text-center">Masuk</h1>
              {expired && !error && (
                <div className="alert alert-info">Sesi kamu sudah berakhir. Silakan login lagi.</div>
              )}
              <ErrorAlert error={error} />
              <form onSubmit={handleSubmit}>
                <div className="mb-3">
                  <label htmlFor="username" className="form-label">
                    Username
                  </label>
                  <input
                    id="username"
                    className="form-control"
                    autoComplete="username"
                    autoFocus
                    required
                    value={form.username}
                    onChange={update('username')}
                  />
                </div>
                <div className="mb-4">
                  <label htmlFor="password" className="form-label">
                    Password
                  </label>
                  <input
                    id="password"
                    type="password"
                    className="form-control"
                    autoComplete="current-password"
                    required
                    value={form.password}
                    onChange={update('password')}
                  />
                </div>
                <button type="submit" className="btn btn-primary w-100" disabled={busy}>
                  {busy ? 'Memproses...' : 'Login'}
                </button>
              </form>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
