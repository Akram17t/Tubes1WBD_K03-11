import { JOB_STATUS, OPERATION, POSITION, REVIEW_STATUS } from './labels.js'

export function ReviewStatusBadge({ status }) {
  const info = REVIEW_STATUS[status] || { label: status, tone: 'secondary' }
  return <span className={`badge text-bg-${info.tone}`}>{info.label}</span>
}

// dipakai buat status job dan execution (enum-nya sama)
export function JobStatusBadge({ status }) {
  const info = JOB_STATUS[status] || { label: status, tone: 'secondary' }
  return <span className={`badge text-bg-${info.tone}`}>{info.label}</span>
}

export function PositionBadge({ position }) {
  const info = POSITION[position] || { label: position, tone: 'light' }
  return <span className={`badge text-bg-${info.tone} border`}>{info.label}</span>
}

export function OperationBadge({ operation }) {
  return <span className="badge text-bg-light border text-uppercase">{OPERATION[operation] || operation}</span>
}

// SQL ditampilin apa adanya; scroll di dalam blok, halaman ga ikut melebar
export function SqlBlock({ sql, label }) {
  if (sql === null || sql === undefined || sql === '') return <p className="text-secondary fst-italic small mb-0">Tidak ada SQL.</p>
  return (
    <pre className="sql-block mb-0" aria-label={label}>
      {sql}
    </pre>
  )
}
