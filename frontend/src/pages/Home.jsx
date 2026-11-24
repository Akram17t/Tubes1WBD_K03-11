import { Link } from 'react-router'
import { useAuth } from '../auth/AuthContext.jsx'

// halaman awal setelah login: sapaan + pintasan ke menu utama
export function Home() {
  const { user } = useAuth()
  return (
    <>
      <h1 className="h3 mb-3">Halo, {user.display_name}</h1>
      <div className="row g-3">
        <Shortcut to="/databases" title="Database" text="Lihat database yang bisa kamu akses dan kelola migration-nya." />
        {user.is_platform_admin && (
          <>
            <Shortcut to="/admin/users" title="User" text="Tambah, ubah, dan nonaktifkan akun." />
            <Shortcut to="/admin/groups" title="Group" text="Atur anggota, leader, dan akses database group." />
            <Shortcut to="/admin/connections" title="Koneksi" text="Daftarkan dan uji koneksi database target." />
          </>
        )}
      </div>
    </>
  )
}

function Shortcut({ to, title, text }) {
  return (
    <div className="col-12 col-sm-6 col-lg-3">
      <Link to={to} className="card h-100 text-decoration-none">
        <div className="card-body">
          <h2 className="h5 card-title">{title}</h2>
          <p className="card-text text-secondary small mb-0">{text}</p>
        </div>
      </Link>
    </div>
  )
}
