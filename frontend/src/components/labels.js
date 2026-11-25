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

// status job & execution. outcome_unknown sengaja bukan merah/"gagal" (kontrak 14)
export const JOB_STATUS = {
  queued: { label: 'Antre', tone: 'secondary' },
  running: { label: 'Berjalan', tone: 'info' },
  success: { label: 'Sukses', tone: 'success' },
  failed: { label: 'Gagal', tone: 'danger' },
  compensated: { label: 'Sudah dikompensasi', tone: 'dark' },
  partial: { label: 'Berhenti di tengah', tone: 'danger' },
  partially_reverted: { label: 'Kompensasi belum tuntas', tone: 'danger' },
  outcome_unknown: { label: 'Hasil belum pasti', tone: 'warning' },
}

// job masih bisa berubah sendiri tanpa aksi user -> perlu di-poll
export const isJobActive = (status) => status === 'queued' || status === 'running'

// posisi definisi migration di database
export const POSITION = {
  never_applied: { label: 'Belum pernah diterapkan', tone: 'light' },
  active: { label: 'Aktif', tone: 'success' },
  rolled_back: { label: 'Sudah di-rollback', tone: 'secondary' },
  superseded: { label: 'Tergantikan', tone: 'dark' },
}

// persen progress dari row yang sudah di-commit; null kalau total belum diketahui
export function progressPercent(progress) {
  if (!progress?.total_rows) return null
  return Math.min(100, Math.round((progress.committed_rows / progress.total_rows) * 100))
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
