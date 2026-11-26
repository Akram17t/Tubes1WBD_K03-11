import { Link } from 'react-router'
import { api } from '../../api/client.js'
import { AccessBadge, BlockerBadge } from '../../components/DatabaseBadges.jsx'
import { PageHeader } from '../../components/PageHeader.jsx'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

// daftar database yang bisa diakses user (admin: semua), plus status job/blocker
export function Databases() {
  const [offset, setOffset] = useOffset()
  const databases = useApi((signal) => api.get('/databases', { limit: PAGE_LIMIT, offset }, { signal }), [offset])

  return (
    <>
      <PageHeader title="Database" />
      {databases.loading && !databases.data ? (
        <Loading />
      ) : databases.error ? (
        <ErrorAlert error={databases.error} onRetry={databases.reload} />
      ) : databases.data.length === 0 ? (
        <Empty text="Kamu belum punya akses ke database mana pun. Minta admin menambahkan group kamu ke database." />
      ) : (
        <div className="list-group">
          {databases.data.map((db) => (
            <Link key={db.id} to={`/databases/${db.id}`} className="list-group-item list-group-item-action d-flex flex-wrap align-items-center gap-2 py-3">
              <span className="fw-semibold me-auto">{db.name}</span>
              {db.active_job_id && !db.block_reason && <span className="badge text-bg-info">Migration berjalan</span>}
              <BlockerBadge reason={db.block_reason} />
              <AccessBadge level={db.access_level} />
            </Link>
          ))}
        </div>
      )}
      <Pagination meta={databases.meta} offset={offset} onChange={setOffset} count={databases.data?.length} />
    </>
  )
}
