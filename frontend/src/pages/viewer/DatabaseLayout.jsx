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
  // halaman turunan ikut nyalain tab induknya: tabel -> Struktur, job -> Request, eksekusi -> History
  const section = pathname.slice(`/databases/${db.id}`.length).split('/')[1] || ''
  const activeTab = { '': '', tables: '', jobs: 'requests', executions: 'history' }[section] ?? section
  const tab = (path) => 'nav-link px-2 px-sm-3' + (activeTab === path ? ' active' : '')
  return (
    <>
      <PageHeader title={db.name} back={{ to: '/databases', label: 'Daftar database' }}>
        <AccessBadge level={db.access_level} />
      </PageHeader>
      <DatabaseStatusAlert database={db} />
      <ul className="nav nav-tabs mb-3 flex-nowrap overflow-x-auto overflow-y-hidden">
        <li className="nav-item">
          <NavLink end to={`/databases/${db.id}`} className={tab('')}>
            Struktur
          </NavLink>
        </li>
        {[
          ['migrations', 'Migration'],
          ['requests', 'Request'],
          ['history', 'History'],
        ].map(([path, label]) => (
          <li className="nav-item" key={path}>
            <NavLink to={`/databases/${db.id}/${path}`} className={tab(path)}>
              {label}
            </NavLink>
          </li>
        ))}
      </ul>
      <Outlet context={{ database: db, reloadDatabase: database.reload }} />
    </>
  )
}
