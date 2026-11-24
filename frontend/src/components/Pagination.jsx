import { useSearchParams } from 'react-router'

export const PAGE_LIMIT = 50

// baca offset dari URL (?offset=50) biar posisi halaman ikut kesimpen waktu refresh
export function useOffset() {
  const [params, setParams] = useSearchParams()
  const offset = Math.max(0, Number.parseInt(params.get('offset') || '0', 10) || 0)
  const setOffset = (next) => {
    const copy = new URLSearchParams(params)
    if (next > 0) copy.set('offset', String(next))
    else copy.delete('offset')
    setParams(copy)
  }
  return [offset, setOffset]
}

// tombol sebelumnya/berikutnya berdasarkan meta.has_more dari server; count = jumlah baris di halaman ini
export function Pagination({ meta, offset, onChange, count }) {
  const limit = meta?.limit || PAGE_LIMIT
  if (!meta || (offset === 0 && !meta.has_more)) return null
  return (
    <nav className="d-flex align-items-center gap-2 mt-3" aria-label="Pagination">
      <button
        type="button"
        className="btn btn-sm btn-outline-secondary"
        disabled={offset === 0}
        onClick={() => onChange(Math.max(0, offset - limit))}
      >
        ‹ Sebelumnya
      </button>
      <span className="small text-secondary">
        Data {offset + 1}–{offset + (count ?? limit)}
      </span>
      <button
        type="button"
        className="btn btn-sm btn-outline-secondary"
        disabled={!meta.has_more}
        onClick={() => onChange(offset + limit)}
      >
        Berikutnya ›
      </button>
    </nav>
  )
}
