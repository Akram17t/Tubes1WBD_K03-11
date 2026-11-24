import { NavLink, Outlet, useLocation, useParams } from 'react-router'
import { api } from '../../api/client.js'
import { AccessBadge, DatabaseStatusAlert } from '../../components/DatabaseBadges.jsx'
import { PageHeader } from '../../components/PageHeader.jsx'
import { ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// kerangka semua halaman di bawah satu database: header, status blocker, dan tab.
// Data database dioper ke halaman anak lewat Outlet context
export function DatabaseLayout() {
  const { databaseId } = useParams()
  const { pathname } = useLocation()
  const database = useApi((signal) => api.get(`/databases/${databaseId}`, null, { signal }), [databaseId])

  if (database.loading && !database.data) return <Loading />
  if (database.error) return <ErrorAlert error={database.error} onRetry={database.reload} />

  const db = database.data
  const tab = ({ isActive }) => 'nav-link' + (isActive ? ' active' : '')
  // tab Struktur tetap aktif waktu lagi buka detail tabel
  const structureActive = pathname === `/databases/${db.id}` || pathname.startsWith(`/databases/${db.id}/tables/`)
  return (
    <>
      <PageHeader title={db.name} back={{ to: '/databases', label: 'Daftar database' }}>
        <AccessBadge level={db.access_level} />
      </PageHeader>
      <DatabaseStatusAlert database={db} />
      <ul className="nav nav-tabs mb-3 flex-nowrap overflow-auto">
        <li className="nav-item">
          <NavLink end to={`/databases/${db.id}`} className={() => tab({ isActive: structureActive })}>
            Struktur
          </NavLink>
        </li>
        <li className="nav-item">
          <NavLink to={`/databases/${db.id}/requests`} className={tab}>
            Request
          </NavLink>
        </li>
      </ul>
      <Outlet context={{ database: db, reloadDatabase: database.reload }} />
    </>
  )
}
