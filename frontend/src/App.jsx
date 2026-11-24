import { Route, Routes } from 'react-router'
import { useAuth } from './auth/AuthContext.jsx'
import { Layout } from './components/Layout.jsx'
import { RequireAdmin, RequireAuth } from './components/RequireAuth.jsx'
import { Home } from './pages/Home.jsx'
import { Login } from './pages/Login.jsx'
import { NotFound } from './pages/NotFound.jsx'

// daftar halaman. key = id user biar state halaman akun lama kebuang waktu ganti akun
export function App() {
  const { user } = useAuth()
  return (
    <Routes key={user?.id ?? 'anon'}>
      <Route path="/login" element={<Login />} />
      <Route element={<RequireAuth />}>
        <Route element={<Layout />}>
          <Route index element={<Home />} />
          <Route path="admin" element={<RequireAdmin />}>
            {/* halaman admin nyusul di tahap B */}
          </Route>
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
