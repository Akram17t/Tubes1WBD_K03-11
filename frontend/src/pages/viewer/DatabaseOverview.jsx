import { Link, useOutletContext, useSearchParams } from 'react-router'
import { api } from '../../api/client.js'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'

const MAX = 500

// ringkasan database: pilih schema (disimpen di ?schema=) lalu daftar tabelnya
export function DatabaseOverview() {
  const { database } = useOutletContext()
  const [params, setParams] = useSearchParams()
  const schema = params.get('schema') || 'public'
  const schemas = useApi((signal) => api.get(`/databases/${database.id}/schemas`, { limit: MAX }, { signal }), [database.id])
  const tables = useApi((signal) => api.get(`/databases/${database.id}/tables`, { schema, limit: MAX }, { signal }), [database.id, schema])

  if (schemas.loading && !schemas.data) return <Loading />
  if (schemas.error) return <ErrorAlert error={schemas.error} onRetry={schemas.reload} />

  return (
    <div className="row g-3">
      <div className="col-md-4 col-lg-3">
        <label htmlFor="schema" className="form-label fw-semibold">
          Schema
        </label>
        <select id="schema" className="form-select" value={schema} onChange={(e) => setParams({ schema: e.target.value })}>
          {!schemas.data.includes(schema) && <option value={schema}>{schema}</option>}
          {schemas.data.map((name) => (
            <option key={name}>{name}</option>
          ))}
        </select>
        <p className="small text-secondary mt-2 mb-0">{schemas.data.length} schema pengguna</p>
      </div>
      <div className="col-md-8 col-lg-9">
        <h2 className="h6 fw-semibold">Tabel di {schema}</h2>
        {tables.loading && !tables.data ? (
          <Loading />
        ) : tables.error ? (
          <ErrorAlert error={tables.error} onRetry={tables.reload} />
        ) : tables.data.length === 0 ? (
          <Empty text="Schema ini belum punya tabel." />
        ) : (
          <div className="list-group">
            {tables.data.map((name) => (
              <Link
                key={name}
                to={`/databases/${database.id}/tables/${encodeURIComponent(schema)}/${encodeURIComponent(name)}`}
                className="list-group-item list-group-item-action font-monospace"
              >
                {name}
              </Link>
            ))}
          </div>
        )}
        {tables.meta?.has_more && <p className="small text-secondary mt-2">Menampilkan {MAX} tabel pertama.</p>}
      </div>
    </div>
  )
}
