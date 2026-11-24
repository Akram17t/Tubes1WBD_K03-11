// Label Indonesia untuk enum dari kontrak + format waktu lokal. Dipakai halaman request/job/history.

export const REVIEW_STATUS = {
  draft: { label: 'Draft', tone: 'secondary' },
  pending_review: { label: 'Menunggu review', tone: 'warning' },
  approved: { label: 'Disetujui', tone: 'success' },
  cancelled: { label: 'Dibatalkan', tone: 'dark' },
}

export const OPERATION = {
  up: 'Up',
  rollback: 'Rollback',
  forward: 'Forward',
  compensate: 'Kompensasi',
}

// timestamp RFC3339 UTC dari server -> waktu lokal browser; null jadi tanda strip
export function formatTime(value) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('id-ID', { dateStyle: 'medium', timeStyle: 'short' })
}

// nama aktor: "Kamu" kalau ID-nya sama dengan user login, selain itu "User #id"
export function actorName(id, me) {
  if (!id) return '—'
  return id === me?.id ? 'Kamu' : `User #${id}`
}
