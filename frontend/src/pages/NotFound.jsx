import { Link } from 'react-router'

export function NotFound() {
  return (
    <div className="py-5 text-center">
      <h1 className="h3">Halaman tidak ditemukan</h1>
      <p className="text-secondary">Alamat yang kamu buka tidak ada.</p>
      <Link to="/" className="btn btn-primary">
        Kembali ke beranda
      </Link>
    </div>
  )
}
