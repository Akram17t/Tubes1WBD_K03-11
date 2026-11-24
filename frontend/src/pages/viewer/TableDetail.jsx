import { Link, useOutletContext, useParams, useSearchParams } from 'react-router'
import { api } from '../../api/client.js'
import { PAGE_LIMIT, Pagination, useOffset } from '../../components/Pagination.jsx'
import { Empty, ErrorAlert, Loading } from '../../components/StateView.jsx'
import { useApi } from '../../hooks/useApi.js'
import { formatCell } from './formatCell.js'

// detail satu tabel: tab Struktur (kolom/constraint/index) dan Data (pagination server).
// Tab aktif disimpen di ?view= biar refresh tetap di tab yang sama
export function TableDetail() {
  const { database } = useOutletContext()
  const { schema, table } = useParams()
  const [params, setParams] = useSearchParams()
  const view = params.get('view') === 'data' ? 'data' : 'structure'
  const tab = (name) => 'nav-link' + (view === name ? ' active' : '')

  return (
    <>
      <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
        <Link to={`/databases/${database.id}?schema=${encodeURIComponent(schema)}`} className="small text-decoration-none">
          ‹ {schema}
        </Link>
        <h2 className="h5 mb-0 font-monospace text-break">
          {schema}.{table}
        </h2>
      </div>
      <ul className="nav nav-pills mb-3">
        <li className="nav-item">
          <button type="button" className={tab('structure')} onClick={() => setParams({})}>
            Struktur
          </button>
        </li>
        <li className="nav-item">
          <button type="button" className={tab('data')} onClick={() => setParams({ view: 'data' })}>
            Data
          </button>
        </li>
      </ul>
      {view === 'data' ? (
        <TableData databaseId={database.id} schema={schema} table={table} />
      ) : (
        <TableStructure databaseId={database.id} schema={schema} table={table} />
      )}
    </>
  )
}

function TableStructure({ databaseId, schema, table }) {
  const detail = useApi((signal) => api.get(`/databases/${databaseId}/table`, { schema, table }, { signal }), [databaseId, schema, table])
  if (detail.loading && !detail.data) return <Loading />
  if (detail.error) return <ErrorAlert error={detail.error} onRetry={detail.reload} />
  const { columns, constraints, indexes } = detail.data

  return (
    <>
      <Section title="Kolom" count={columns.length}>
        <SimpleTable
          head={['Nama', 'Tipe PostgreSQL', 'Boleh NULL']}
          rows={columns.map((c) => [c.name, c.data_type, c.nullable ? 'Ya' : 'Tidak'])}
          empty="Tabel ini tidak punya kolom."
        />
      </Section>
      <Section title="Constraint" count={constraints.length}>
        <SimpleTable head={['Nama', 'Jenis', 'Definisi']} rows={constraints.map((c) => [c.name, c.type, c.definition])} empty="Tidak ada constraint." />
      </Section>
      <Section title="Index" count={indexes.length}>
        <SimpleTable head={['Nama', 'Definisi']} rows={indexes.map((i) => [i.name, i.definition])} empty="Tidak ada index." />
      </Section>
    </>
  )
}

// isi tabel dengan pagination dari server; offset disimpen di URL
function TableData({ databaseId, schema, table }) {
  const [offset, setOffset] = useOffset()
  const data = useApi(
    (signal) => api.get(`/databases/${databaseId}/data`, { schema, table, limit: PAGE_LIMIT, offset }, { signal }),
    [databaseId, schema, table, offset],
  )
  if (data.loading && !data.data) return <Loading />
  if (data.error) return <ErrorAlert error={data.error} onRetry={data.reload} />
  const { columns, rows, limit, has_more } = data.data

  return (
    <>
      {rows.length === 0 ? (
        <Empty text={offset > 0 ? 'Tidak ada data lagi di halaman ini.' : 'Tabel ini masih kosong.'} />
      ) : (
        <div className="table-responsive border rounded bg-white" style={{ maxHeight: '70vh' }}>
          <table className="table table-sm table-striped mb-0 small">
            <thead className="sticky-top">
              <tr>
                {columns.map((name) => (
                  <th key={name} className="font-monospace text-nowrap">
                    {name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={offset + i}>
                  {row.map((value, j) => {
                    const cell = formatCell(value)
                    return (
                      <td key={j} className={'cell-value' + (cell.isNull ? ' text-secondary fst-italic' : '')} title={cell.text}>
                        {cell.text}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pagination meta={{ limit, has_more }} offset={offset} onChange={setOffset} count={rows.length} />
    </>
  )
}

function Section({ title, count, children }) {
  return (
    <section className="mb-4">
      <h3 className="h6 fw-semibold">
        {title} <span className="badge text-bg-light border">{count}</span>
      </h3>
      {children}
    </section>
  )
}

function SimpleTable({ head, rows, empty }) {
  if (rows.length === 0) return <Empty text={empty} />
  return (
    <div className="table-responsive border rounded bg-white">
      <table className="table table-sm mb-0 small">
        <thead>
          <tr>
            {head.map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((cells, i) => (
            <tr key={i}>
              {cells.map((cell, j) => (
                <td key={j} className={j === 0 ? 'font-monospace text-nowrap' : 'text-break'}>
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
