import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router'
import { useAuth } from '../auth/AuthContext.jsx'

const navClass = ({ isActive }) => 'nav-link' + (isActive ? ' active' : '')

// kerangka semua halaman setelah login: navbar (bisa dilipat di HP) + isi halaman
export function Layout() {
  const { user, logout } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  // tombol dikunci selama request biar ga double klik
  async function handleLogout() {
    setBusy(true)
    try {
      await logout()
    } finally {
      setBusy(false)
      navigate('/login', { replace: true })
    }
  }

  return (
    <>
      <nav className="navbar navbar-expand-md navbar-dark bg-dark mb-4">
        <div className="container-xl">
          <NavLink className="navbar-brand" to="/">
            DB Platform
          </NavLink>
          <button
            className="navbar-toggler"
            type="button"
            aria-controls="main-nav"
            aria-expanded={open}
            aria-label="Buka menu"
            onClick={() => setOpen((v) => !v)}
          >
            <span className="navbar-toggler-icon" />
          </button>
          <div id="main-nav" className={'collapse navbar-collapse' + (open ? ' show' : '')} onClick={() => setOpen(false)}>
            <ul className="navbar-nav me-auto">
              <li className="nav-item">
                <NavLink className={navClass} to="/databases">
                  Database
                </NavLink>
              </li>
              {user?.is_platform_admin && (
                <>
                  <li className="nav-item">
                    <NavLink className={navClass} to="/admin/users">
                      User
                    </NavLink>
                  </li>
                  <li className="nav-item">
                    <NavLink className={navClass} to="/admin/groups">
                      Group
                    </NavLink>
                  </li>
                  <li className="nav-item">
                    <NavLink className={navClass} to="/admin/connections">
                      Koneksi
                    </NavLink>
                  </li>
                </>
              )}
            </ul>
            <div className="d-flex align-items-center gap-2 py-2 py-md-0">
              <span className="navbar-text text-truncate">
                {user?.display_name}
                {user?.is_platform_admin && <span className="badge text-bg-warning ms-2">Admin</span>}
              </span>
              <button type="button" className="btn btn-outline-light btn-sm" onClick={handleLogout} disabled={busy}>
                {busy ? 'Keluar...' : 'Logout'}
              </button>
            </div>
          </div>
        </div>
      </nav>
      <main className="container-xl pb-5">
        <Outlet />
      </main>
    </>
  )
}
