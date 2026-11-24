// Mock API khusus dev (`npm run dev:mock`). Ngejawab endpoint workflow yang belum
// tersambung di backend pakai data contoh sesuai kontrak; endpoint lain tetap ke Go.
// Ga ikut ke build production. Hapus begitu endpoint asli sudah siap semua.

const now = () => new Date().toISOString()
const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString()

// snapshot definisi migration yang dibaca leader waktu review
function snapshot(id, title, extra = {}) {
  return {
    migration_id: String(id),
    database_id: '1',
    revision: 1,
    title,
    description: null,
    sql_up: `ALTER TABLE public.players ADD COLUMN nickname text`,
    sql_down: `ALTER TABLE public.players DROP COLUMN nickname`,
    irreversible: false,
    irreversible_reason: null,
    mode: 'normal',
    batch_config: null,
    ...extra,
  }
}

function seed() {
  const requests = [
    {
      id: '41', migration_id: '12', revision: 1, operation: 'up', review_status: 'draft', requested_by: '1',
      snapshot: snapshot(12, 'Tambah kolom nickname', { description: 'Nama panggilan pemain untuk aplikasi mobile.' }),
      created_at: ago(30), updated_at: ago(30), submitted_at: null,
      approvals: [],
    },
    {
      id: '42', migration_id: '13', revision: 2, operation: 'up', review_status: 'pending_review', requested_by: '1',
      snapshot: snapshot(13, 'Isi ulang kolom rating per batch', {
        revision: 2,
        mode: 'batch',
        sql_up: `UPDATE public.players SET rating = 0 WHERE id > $1 AND id <= $2`,
        sql_down: `UPDATE public.players SET rating = NULL WHERE id > $1 AND id <= $2`,
        batch_config: { table: 'public.players', key_column: 'id', batch_size: 1000 },
      }),
      created_at: ago(120), updated_at: ago(60), submitted_at: ago(60),
      approvals: [
        { group_id: '1', group_name: 'analyst', approved_by: '2', approved_at: ago(50), waiting_for_leader: false, can_approve: false },
        { group_id: '2', group_name: 'developer', approved_by: null, approved_at: null, waiting_for_leader: false, can_approve: true },
        { group_id: '3', group_name: 'tim-mobile', approved_by: null, approved_at: null, waiting_for_leader: true, can_approve: false },
      ],
    },
    {
      id: '43', migration_id: '14', revision: 1, operation: 'up', review_status: 'approved', requested_by: '1',
      snapshot: snapshot(14, 'Hapus tabel log lama', {
        sql_up: 'DROP TABLE public.old_logs',
        sql_down: null,
        irreversible: true,
        irreversible_reason: 'Data log lama sudah diarsipkan ke storage terpisah.',
      }),
      created_at: ago(300), updated_at: ago(200), submitted_at: ago(250),
      approvals: [{ group_id: '2', group_name: 'developer', approved_by: '1', approved_at: ago(200), waiting_for_leader: false, can_approve: false }],
    },
    {
      id: '44', migration_id: '12', revision: 1, operation: 'rollback', target_execution_id: '7', review_status: 'cancelled', requested_by: '3',
      snapshot: snapshot(12, 'Tambah kolom nickname'),
      created_at: ago(900), updated_at: ago(880), submitted_at: null,
      approvals: [],
    },
  ]
  return { requests, jobs: new Map(), nextJob: 81 }
}

let state = seed()

function send(res, status, data, extraMeta = {}) {
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  res.end(JSON.stringify({ data, meta: { request_id: 'mock-' + Math.random().toString(16).slice(2, 10), ...extraMeta } }))
}

function fail(res, status, code, message, details) {
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  res.end(JSON.stringify({ error: { code, message, ...(details ? { details } : {}) }, meta: { request_id: 'mock-err' } }))
}

// bentuk list: MigrationRequest tanpa approval
function summary(r) {
  const { approvals: _a, job_id: _j, ...rest } = r
  return { database_id: '1', target_execution_id: null, source_job_id: null, ...rest }
}

// bentuk detail + allowed_actions versi sederhana (mock anggap actor = pengaju & leader developer)
function detail(r) {
  const pending = r.approvals.filter((a) => !a.approved_by)
  const canApprove = r.review_status === 'pending_review' && r.approvals.some((a) => a.can_approve && !a.approved_by)
  const actions = []
  if (canApprove) actions.push('approve')
  if (r.review_status === 'approved' && pending.length === 0 && !r.job_id) actions.push('execute')
  if (r.review_status !== 'cancelled' && !r.job_id) actions.push('cancel')
  if (r.review_status === 'draft') actions.push('submit')
  return {
    ...summary(r),
    approvals: r.approvals.map((a) => ({ ...a, can_approve: r.review_status === 'pending_review' && a.can_approve && !a.approved_by })),
    pending_group_ids: pending.map((a) => a.group_id),
    can_approve: canApprove,
    allowed_actions: actions,
    job_id: r.job_id || null,
  }
}

function job(r) {
  return {
    id: r.job_id, request_id: r.id, database_id: '1', migration_id: r.migration_id, revision: r.revision, operation: r.operation,
    status: 'queued', attempt_count: 0,
    progress: { total_rows: null, committed_rows: 0, committed_batches: 0, reverted_rows: 0, reverted_batches: 0, last_cursor: null },
    blocking_database: false, allowed_actions: [], status_url: `/api/databases/1/jobs/${r.job_id}`, error: null,
  }
}

const ROUTE = /^\/api\/databases\/(\d+)\/requests(?:\/(\d+))?(?:\/(submit|approve|cancel|execute))?\/?$/

function handle(req, res, next) {
  const url = new URL(req.url, 'http://mock')
  if (url.pathname === '/api/__mock/reset' && req.method === 'POST') {
    state = seed()
    return send(res, 200, null)
  }
  const match = url.pathname.match(ROUTE)
  if (!match) return next()
  const [, , requestId, action] = match
  // jeda kecil biar state loading/tombol terkunci kelihatan
  setTimeout(() => {
    if (!requestId) {
      if (req.method !== 'GET') return fail(res, 503, 'workflow_unavailable', 'Mock belum mendukung membuat request dari sini.')
      const limit = Number(url.searchParams.get('limit') || 50)
      const offset = Number(url.searchParams.get('offset') || 0)
      const all = [...state.requests].sort((a, b) => b.created_at.localeCompare(a.created_at))
      return send(res, 200, all.slice(offset, offset + limit).map(summary), { limit, offset, has_more: offset + limit < all.length })
    }
    const r = state.requests.find((x) => x.id === requestId)
    if (!r) return fail(res, 404, 'not_found', 'Pengajuan atau migration tidak ditemukan.')
    if (req.method === 'GET' && !action) return send(res, 200, detail(r))
    if (req.method !== 'POST') return fail(res, 404, 'not_found', 'Resource tidak ditemukan.')
    const allowed = detail(r).allowed_actions
    if (action === 'execute' && r.job_id) return send(res, 202, job(r)) // klik dobel -> job yang sama
    if (!allowed.includes(action)) {
      if (action === 'execute' && r.approvals.some((a) => !a.approved_by)) {
        return fail(res, 409, 'approval_incomplete', 'Masih menunggu persetujuan group.', { pending_group_ids: detail(r).pending_group_ids })
      }
      return fail(res, 409, 'state_conflict', 'Status atau revisi pengajuan tidak sesuai.')
    }
    r.updated_at = now()
    if (action === 'submit') {
      r.review_status = 'pending_review'
      r.submitted_at = now()
      r.approvals = [{ group_id: '2', group_name: 'developer', approved_by: null, approved_at: null, waiting_for_leader: false, can_approve: true }]
      return send(res, 200, summary(r))
    }
    if (action === 'approve') {
      for (const a of r.approvals) if (a.can_approve && !a.approved_by) Object.assign(a, { approved_by: '1', approved_at: now(), can_approve: false })
      if (r.approvals.every((a) => a.approved_by)) r.review_status = 'approved'
      return send(res, 200, { request_id: r.id, review_status: r.review_status, approved_group_ids: r.approvals.filter((a) => a.approved_by).map((a) => a.group_id) })
    }
    if (action === 'cancel') {
      r.review_status = 'cancelled'
      return send(res, 200, summary(r))
    }
    r.job_id = String(state.nextJob++)
    return send(res, 202, job(r))
  }, 250)
}

export function mockApi() {
  return {
    name: 'tubes-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(handle)
      server.config.logger.info('  ➜  Mock API aktif untuk /api/databases/:id/requests')
    },
  }
}
