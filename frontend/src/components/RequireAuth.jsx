import { Navigate, Outlet, useLocation } from 'react-router'
import { useAuth } from '../auth/AuthContext.jsx'
import { ErrorAlert, Forbidden, Loading } from './StateView.jsx'

// pagar route yang butuh login: belum login -> ke /login sambil bawa alamat asal
export function RequireAuth() {
  const { status, error, refresh } = useAuth()
  const location = useLocation()
  if (status === 'loading') return <Loading text="Memeriksa sesi..." />
  if (status === 'error') return <ErrorAlert error={error} onRetry={refresh} />
  if (status !== 'authenticated') {
    const next = location.pathname + location.search
    return <Navigate to={`/login?next=${encodeURIComponent(next)}`} replace />
  }
  return <Outlet />
}

// pagar halaman admin; backend tetap ngecek ulang, ini cuma biar UI ga nampilin halaman kosong
export function RequireAdmin() {
  const { user } = useAuth()
  if (!user?.is_platform_admin) return <Forbidden text="Halaman ini hanya untuk Platform Admin." />
  return <Outlet />
}
