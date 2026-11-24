import { Link } from 'react-router'

// judul halaman + link balik (opsional) + tombol aksi di kanan
export function PageHeader({ title, back, children }) {
  return (
    <div className="d-flex flex-wrap align-items-center gap-2 mb-3">
      <div className="me-auto">
        {back && (
          <Link to={back.to} className="small text-decoration-none d-block mb-1">
            ‹ {back.label}
          </Link>
        )}
        <h1 className="h3 mb-0">{title}</h1>
      </div>
      {children}
    </div>
  )
}

export function StatusBadge({ active, on = 'Aktif', off = 'Nonaktif' }) {
  return <span className={'badge ' + (active ? 'text-bg-success' : 'text-bg-secondary')}>{active ? on : off}</span>
}
