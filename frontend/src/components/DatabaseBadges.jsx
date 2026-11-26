// Label akses dan blocker database, dipakai di daftar & header database.

export function AccessBadge({ level }) {
  if (level === 'edit') return <span className="badge text-bg-primary">Edit</span>
  if (level === 'view') return <span className="badge text-bg-secondary">View</span>
  return <span className="badge text-bg-light border">{level || '—'}</span>
}

// penjelasan blocker. outcome_unknown sengaja ga disebut "gagal" dan ga nyuruh jalanin SQL manual
const BLOCKERS = {
  partial: {
    tone: 'danger',
    title: 'Migration berhenti di tengah',
    text: 'Sebagian batch sudah di-commit. Operasi migration lain ditahan sampai job ini dilanjutkan (resume) atau dikompensasi.',
  },
  partially_reverted: {
    tone: 'danger',
    title: 'Kompensasi belum tuntas',
    text: 'Sebagian perubahan sudah dibatalkan. Operasi migration lain ditahan sampai kompensasi dilanjutkan.',
  },
  outcome_unknown: {
    tone: 'warning',
    title: 'Hasil eksekusi belum bisa dipastikan',
    text: 'Server belum bisa memastikan apakah perubahan terakhir sudah di-commit. Database ditahan sampai sistem selesai memeriksa. Jangan menjalankan ulang SQL secara manual.',
  },
}

export function BlockerBadge({ reason }) {
  if (!reason) return null
  const info = BLOCKERS[reason]
  return <span className={`badge text-bg-${info?.tone || 'danger'}`}>{info?.title || reason}</span>
}

// kotak penjelasan di halaman database: blocker atau job yang lagi jalan
export function DatabaseStatusAlert({ database }) {
  if (database.block_reason) {
    const info = BLOCKERS[database.block_reason] || { tone: 'danger', title: database.block_reason, text: '' }
    return (
      <div className={`alert alert-${info.tone}`} role="alert">
        <strong>{info.title}.</strong> {info.text}
        {database.blocking_job_id && <span className="d-block small mt-1 font-monospace">Job #{database.blocking_job_id}</span>}
      </div>
    )
  }
  if (database.active_job_id) {
    return (
      <div className="alert alert-info" role="status">
        Ada migration yang sedang diproses di database ini <span className="font-monospace">(job #{database.active_job_id})</span>. Operasi
        migration lain akan menunggu giliran.
      </div>
    )
  }
  return null
}
