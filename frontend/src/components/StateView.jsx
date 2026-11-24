// Komponen state standar: loading, error, kosong, forbidden. Dipakai semua halaman biar seragam.

export function Loading({ text = 'Memuat...' }) {
  return (
    <div className="d-flex align-items-center gap-2 text-secondary py-4" role="status">
      <span className="spinner-border spinner-border-sm" aria-hidden="true" />
      <span>{text}</span>
    </div>
  )
}

// pesan error dari API + request_id biar gampang dicocokin sama log server
export function ErrorAlert({ error, onRetry }) {
  if (!error) return null
  return (
    <div className="alert alert-danger d-flex flex-wrap align-items-center gap-2" role="alert">
      <span className="me-auto">{error.message || 'Terjadi kesalahan.'}</span>
      {error.requestId && <small className="text-body-secondary font-monospace">ID: {error.requestId}</small>}
      {onRetry && (
        <button type="button" className="btn btn-sm btn-outline-danger" onClick={onRetry}>
          Coba lagi
        </button>
      )}
    </div>
  )
}

export function Empty({ text = 'Belum ada data.' }) {
  return <p className="text-secondary fst-italic py-3 mb-0">{text}</p>
}

export function Forbidden({ text = 'Kamu tidak punya akses ke halaman ini.' }) {
  return (
    <div className="alert alert-warning" role="alert">
      <strong>Akses ditolak.</strong> {text}
    </div>
  )
}
