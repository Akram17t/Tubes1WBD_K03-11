import { useEffect, useState } from 'react'

// tombol aksi berbahaya dua langkah: klik pertama minta konfirmasi, klik kedua baru jalan.
// Ga pakai window.confirm biar tetap jalan di HP dan ga ngeblok halaman
export function ConfirmButton({ onConfirm, busy, children, confirmText = 'Yakin?', className = 'btn btn-sm btn-outline-danger', disabled }) {
  const [asking, setAsking] = useState(false)

  // konfirmasi otomatis batal kalau dibiarin 4 detik
  useEffect(() => {
    if (!asking) return undefined
    const timer = setTimeout(() => setAsking(false), 4000)
    return () => clearTimeout(timer)
  }, [asking])

  if (!asking) {
    return (
      <button type="button" className={className} disabled={disabled || busy} onClick={() => setAsking(true)}>
        {children}
      </button>
    )
  }
  return (
    <span className="d-inline-flex gap-1">
      <button
        type="button"
        className="btn btn-sm btn-danger"
        disabled={busy}
        onClick={async () => {
          // error ditampilin sama halaman pemanggil (lewat useAction), di sini cukup tutup konfirmasi
          try {
            await onConfirm()
          } catch {
            /* sudah ditangani pemanggil */
          }
          setAsking(false)
        }}
      >
        {busy ? 'Memproses...' : confirmText}
      </button>
      <button type="button" className="btn btn-sm btn-outline-secondary" disabled={busy} onClick={() => setAsking(false)}>
        Batal
      </button>
    </span>
  )
}
