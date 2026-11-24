// Komponen state standar: loading, error, kosong, forbidden. Dipakai semua halaman biar seragam.

export function Loading({ text = 'Memuat...' }) {
  return (
    <div className="d-flex align-items-center gap-2 text-secondary py-4" role="status">
      <span className="spinner-border spinner-border-sm" aria-hidden="true" />
      <span>{text}</span>
    </div>
  )
}

// pesan error dari API + request_id biar gampang dicocokin sama log server.
// 403 tampil sebagai "akses ditolak"; tombol coba lagi cuma buat error jaringan/server (5xx)
export function ErrorAlert({ error, onRetry }) {
  if (!error) return null
  const forbidden = error.status === 403 && error.code !== 'csrf_invalid'
  const retryable = !error.status || error.status >= 500
  return (
    <div className={'alert d-flex flex-wrap align-items-center gap-2 ' + (forbidden ? 'alert-warning' : 'alert-danger')} role="alert">
      <span className="me-auto">
        {forbidden && <strong>Akses ditolak. </strong>}
        {forbidden && error.message === 'Akses ditolak.' ? 'Kamu tidak punya izin untuk ini.' : error.message || 'Terjadi kesalahan.'}
      </span>
      {error.requestId && <small className="text-body-secondary font-monospace">ID: {error.requestId}</small>}
      {onRetry && retryable && (
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
