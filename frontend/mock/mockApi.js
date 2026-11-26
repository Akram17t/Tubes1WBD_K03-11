// Mock API khusus dev (`npm run dev:mock`). Ngejawab endpoint migration/request/job/history yang
// belum ada di backend pakai data contoh sesuai kontrak; endpoint lain tetap ke Go.
// Ga ikut ke build production. Hapus begitu endpoint asli sudah siap semua.

const now = () => new Date().toISOString()
const ago = (minutes) => new Date(Date.now() - minutes * 60000).toISOString()
const ME = '1' // mock anggap actor = user #1 dengan akses Edit & leader developer
const ACTIVE = ['queued', 'running']
const BLOCKING = ['partial', 'partially_reverted', 'outcome_unknown']

// definisi migration; field sesuai kontrak 9.4
function migration(id, title, extra = {}) {
  return {
    id: String(id), database_id: '1', revision: 1, title, description: null,
    sql_up: 'ALTER TABLE public.players ADD COLUMN nickname text',
    sql_down: 'ALTER TABLE public.players DROP COLUMN nickname',
    irreversible: false, irreversible_reason: null, mode: 'normal', batch_config: null,
    created_by: ME, created_at: ago(2000 - id * 10), updated_at: ago(2000 - id * 10), locked_at: ago(1000),
    ...extra,
  }
}

const BATCH_DEMO = {
  mode: 'batch',
  batch_config: { schema: 'public', table: 'batch_demo', key_column: 'id', batch_size: 1000 },
  sql_up: 'UPDATE public.batch_demo SET value = value + 1 WHERE id = ANY($1::bigint[])',
  sql_down: 'UPDATE public.batch_demo SET value = value - 1 WHERE id = ANY($1::bigint[])',
}

// satu attempt eksekusi; field sesuai migration_executions (sql/015)
function execution(id, migrationId, operation, status, minutes, extra = {}) {
  return {
    id: String(id), job_id: null, request_id: null, migration_id: String(migrationId), revision: 1, operation, attempt: 1, status,
    target_execution_id: null, history_sequence: null, executed_by: ME, sql_snapshot: null,
    error_code: null, sqlstate: null, error_message: null, started_at: ago(minutes), finished_at: ago(minutes - 1),
    progress: { total_rows: null, committed_rows: 0, committed_batches: 0, reverted_rows: 0, reverted_batches: 0, last_cursor: null },
    batch_attempts: [],
    ...extra,
  }
}

function batch(id, executionId, number, status, extra = {}) {
  return {
    id: String(id), execution_id: String(executionId), batch_number: number, direction: 'up',
    first_id: String((number - 1) * 1000 + 1), last_id: String(number * 1000), row_count: status === 'success' ? 1000 : 0,
    sql_snapshot: BATCH_DEMO.sql_up, status, error_message: null, ...extra,
  }
}

function seed() {
  const migrations = [
    migration(9, 'Buat tabel clubs_archive', { sql_up: 'CREATE TABLE public.clubs_archive (LIKE public.clubs)', sql_down: 'DROP TABLE public.clubs_archive' }),
    migration(10, 'Tambah index players(name)', { sql_up: 'CREATE INDEX players_name_idx ON public.players (name)', sql_down: 'DROP INDEX public.players_name_idx' }),
    migration(11, 'Tambah kolom joined_at', { sql_up: 'ALTER TABLE public.players ADD COLUMN joined_at date', sql_down: 'ALTER TABLE public.players DROP COLUMN joined_at' }),
    migration(12, 'Tambah kolom nickname', { description: 'Nama panggilan pemain untuk aplikasi mobile.' }),
    migration(13, 'Isi ulang kolom rating per batch', { ...BATCH_DEMO, revision: 2 }),
    migration(14, 'Hapus tabel log lama', {
      sql_up: 'DROP TABLE public.old_logs', sql_down: null, irreversible: true, irreversible_reason: 'Data log lama sudah diarsipkan ke storage terpisah.',
    }),
    migration(15, 'Naikkan value batch_demo', BATCH_DEMO),
    migration(16, 'Ubah tipe kolom score', { sql_up: 'ALTER TABLE public.players ALTER COLUMN score TYPE integer', sql_down: 'ALTER TABLE public.players ALTER COLUMN score TYPE text' }),
    migration(18, 'Tambah constraint rating positif', { sql_up: 'ALTER TABLE public.players ADD CONSTRAINT rating_positive CHECK (rating >= 0)', sql_down: 'ALTER TABLE public.players DROP CONSTRAINT rating_positive' }),
    migration(17, 'Tambah tabel sponsors', {
      sql_up: 'CREATE TABLE public.sponsors (id bigserial PRIMARY KEY, name text NOT NULL)', sql_down: 'DROP TABLE public.sponsors', locked_at: null,
    }),
  ]
  const sqlOf = (id, op) => migrations.find((m) => m.id === String(id))[op === 'rollback' || op === 'compensate' ? 'sql_down' : 'sql_up']

  // contoh wajib kontrak 10: E1 A up, E2 B up, E3 rollback E2, E4 forward B, E5 C up
  const executions = [
    execution(1, 9, 'up', 'success', 900, { job_id: '70', history_sequence: '1' }),
    execution(2, 10, 'up', 'success', 800, { job_id: '71', history_sequence: '2' }),
    execution(3, 10, 'rollback', 'success', 700, { job_id: '72', history_sequence: '3', target_execution_id: '2' }),
    execution(4, 10, 'forward', 'success', 600, { job_id: '73', history_sequence: '4', target_execution_id: '2' }),
    execution(5, 11, 'up', 'success', 500, { job_id: '74', history_sequence: '5' }),
    execution(6, 16, 'up', 'failed', 400, { job_id: '78', error_code: 'retryable_error', sqlstate: '40P01', error_message: 'Deadlock terdeteksi, transaction di-rollback. Dicoba ulang otomatis.' }),
    execution(7, 16, 'up', 'failed', 398, { job_id: '78', attempt: 2, error_code: 'sql_error', sqlstate: '42804', error_message: 'Kolom "score" tidak bisa diubah otomatis ke tipe integer.' }),
    execution(8, 15, 'up', 'partial', 300, {
      job_id: '79', error_code: 'target_unavailable', error_message: 'Koneksi ke database target terputus sebelum batch 4 di-commit.',
      progress: { total_rows: 10000, committed_rows: 3000, committed_batches: 3, reverted_rows: 0, reverted_batches: 0, last_cursor: '3000' },
      batch_attempts: [batch(1, 8, 1, 'success'), batch(2, 8, 2, 'success'), batch(3, 8, 3, 'success'), batch(4, 8, 4, 'failed', { error_message: 'Koneksi terputus.' })],
    }),
    execution(9, 18, 'up', 'outcome_unknown', 200, { job_id: '80', finished_at: null }),
  ]
  for (const e of executions) e.sql_snapshot = sqlOf(e.migration_id, e.operation)

  const job = (id, requestId, e, extra = {}) => ({
    id: String(id), request_id: String(requestId), database_id: '1', migration_id: e.migration_id, revision: e.revision, operation: e.operation,
    status: e.status, attempt_count: e.attempt, progress: { ...e.progress }, error: e.error_message ? { code: e.error_code, message: e.error_message, sqlstate: e.sqlstate } : null,
    execution_actor_id: ME, queued_at: e.started_at, finished_at: e.finished_at, next_attempt_at: null, compensation_job_id: null, ...extra,
  })
  const jobs = new Map(
    [
      job(70, 30, executions[0]), job(71, 31, executions[1]), job(72, 32, executions[2]), job(73, 33, executions[3]), job(74, 34, executions[4]),
      job(78, 38, executions[6], { attempt_count: 2 }), job(79, 39, executions[7]), job(80, 40, executions[8]),
    ].map((j) => [j.id, j]),
  )

  const req = (id, m, operation, review_status, extra = {}) => ({
    id: String(id), migration_id: m.id, revision: m.revision, operation, review_status, requested_by: ME,
    target_execution_id: null, source_job_id: null, snapshot: snapshotOf(m),
    created_at: ago(1000 - id), updated_at: ago(1000 - id), submitted_at: ago(990 - id), approvals: [], ...extra,
  })
  const byId = (id) => migrations.find((m) => m.id === String(id))
  const approvedBy = { group_id: '2', group_name: 'developer', approved_by: ME, approved_at: ago(500), waiting_for_leader: false, can_approve: false }
  const requests = [
    req(38, byId(16), 'up', 'approved', { approvals: [approvedBy], job_id: '78' }),
    req(39, byId(15), 'up', 'approved', { approvals: [approvedBy], job_id: '79' }),
    req(40, byId(18), 'up', 'approved', { approvals: [approvedBy], job_id: '80' }),
    req(41, byId(12), 'up', 'draft', { submitted_at: null, created_at: ago(30), updated_at: ago(30) }),
    req(42, byId(13), 'up', 'pending_review', {
      created_at: ago(120), submitted_at: ago(60),
      approvals: [
        { group_id: '1', group_name: 'analyst', approved_by: '2', approved_at: ago(50), waiting_for_leader: false, can_approve: false },
        { group_id: '2', group_name: 'developer', approved_by: null, approved_at: null, waiting_for_leader: false, can_approve: true },
        { group_id: '3', group_name: 'tim-mobile', approved_by: null, approved_at: null, waiting_for_leader: true, can_approve: false },
      ],
    }),
    req(43, byId(14), 'up', 'approved', { created_at: ago(300), approvals: [{ ...approvedBy, approved_at: ago(200) }] }),
    req(44, byId(11), 'rollback', 'cancelled', { target_execution_id: '5', requested_by: '3', submitted_at: null, created_at: ago(90) }),
  ]
  return { migrations, executions, jobs, requests, nextJob: 81, nextRequest: 45, nextMigration: 19, nextExecution: 10, nextBatch: 5 }
}

const snapshotOf = (m) => {
  const { id, created_by: _c, created_at: _a, updated_at: _u, locked_at: _l, ...rest } = m
  return { migration_id: id, ...rest }
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

function page(res, url, all) {
  const limit = Math.min(500, Number(url.searchParams.get('limit') || 50))
  const offset = Number(url.searchParams.get('offset') || 0)
  return send(res, 200, all.slice(offset, offset + limit), { limit, offset, has_more: offset + limit < all.length })
}

// Hide rollback (kontrak 10): sembunyiin rollback sukses R + execution sukses E yang ditunjuk R.
// Pasangan dicari dari seluruh history dulu, baru hasilnya difilter/paginate
export function hideRollback(executions) {
  const byId = new Map(executions.map((e) => [e.id, e]))
  const hidden = new Set()
  for (const r of executions) {
    const target = byId.get(r.target_execution_id)
    if (r.operation === 'rollback' && r.status === 'success' && target?.status === 'success') hidden.add(r.id).add(target.id)
  }
  return executions.filter((e) => !hidden.has(e.id))
}

// active stack & forward stack dari event sukses (kontrak 5.3) -> posisi tiap migration
export function positions(executions) {
  const active = []
  let forward = []
  const applied = new Set()
  for (const e of executions) {
    if (e.status !== 'success' || e.operation === 'compensate') continue
    if (e.operation === 'up') {
      active.push(e)
      forward = []
    } else if (e.operation === 'rollback') {
      forward.push(active.pop())
    } else if (e.operation === 'forward') {
      forward.pop()
      active.push(e)
    }
    applied.add(e.migration_id)
  }
  return { active, forward, applied }
}

function migrationDto(m) {
  const { active, forward, applied } = positions(state.executions)
  const top = active.at(-1)
  const candidate = forward.at(-1)
  const position = active.some((e) => e.migration_id === m.id) ? 'active'
    : forward.some((e) => e.migration_id === m.id) ? 'rolled_back'
    : applied.has(m.id) ? 'superseded' : 'never_applied'
  const busy = state.requests.some((r) => r.migration_id === m.id && !r.job_id && r.review_status !== 'cancelled')
  const blocked = [...state.jobs.values()].some((j) => j.migration_id === m.id)
  const actions = []
  if (!m.locked_at) actions.push('edit')
  if (position === 'never_applied' && !busy && !blocked) actions.push('request_up')
  if (top?.migration_id === m.id && !m.irreversible && !busy) actions.push('request_rollback')
  if (candidate?.migration_id === m.id && !busy) actions.push('request_forward')
  return {
    ...m, position, allowed_actions: actions,
    active_execution_id: top?.migration_id === m.id ? top.id : null,
    forward_execution_id: candidate?.migration_id === m.id ? candidate.id : null,
  }
}

// cek field wajib definisi; error 400 bawa details.field biar input-nya bisa ditandai merah
function invalidMigration(body) {
  if (!body.title?.trim()) return 'title'
  if (!body.sql_up?.trim()) return 'sql_up'
  if (body.irreversible && !body.irreversible_reason?.trim()) return 'irreversible_reason'
  if (!body.irreversible && !body.sql_down?.trim()) return 'sql_down'
  if (body.mode === 'batch' && !body.batch_config?.table) return 'batch_config.table'
  if (body.mode === 'batch' && !/\$1/.test(body.sql_up)) return 'sql_up'
  return null
}

// ---------- request (kontrak 9.5) ----------

function requestSummary(r) {
  const { approvals: _a, job_id: _j, ...rest } = r
  return { database_id: '1', ...rest }
}

function requestDetail(r) {
  const pending = r.approvals.filter((a) => !a.approved_by)
  const canApprove = r.review_status === 'pending_review' && r.approvals.some((a) => a.can_approve && !a.approved_by)
  const actions = []
  if (canApprove) actions.push('approve')
  if (r.review_status === 'approved' && pending.length === 0 && !r.job_id) actions.push('execute')
  if (r.review_status !== 'cancelled' && !r.job_id) actions.push('cancel')
  if (r.review_status === 'draft') actions.push('submit')
  return {
    ...requestSummary(r),
    approvals: r.approvals.map((a) => ({ ...a, can_approve: r.review_status === 'pending_review' && a.can_approve && !a.approved_by })),
    pending_group_ids: pending.map((a) => a.group_id),
    can_approve: canApprove,
    allowed_actions: actions,
    job_id: r.job_id || null,
  }
}

function createRequest(res, body) {
  const m = state.migrations.find((x) => x.id === String(body.migration_id))
  if (!m || m.revision !== body.revision) return fail(res, 409, 'state_conflict', 'Migration atau revisi tidak cocok.')
  if (body.operation === 'compensate') {
    const source = state.jobs.get(String(body.source_job_id))
    if (!source || source.status !== 'partial') return fail(res, 409, 'state_conflict', 'Job sumber tidak bisa dikompensasi.')
    const existing = state.requests.find((r) => r.source_job_id === source.id && r.review_status !== 'cancelled')
    if (existing) return fail(res, 409, 'state_conflict', 'Kompensasi untuk job ini sudah diajukan.', { existing_request_id: existing.id })
  } else if (!migrationDto(m).allowed_actions.includes(`request_${body.operation}`)) {
    return fail(res, 409, 'state_conflict', 'Operasi ini tidak bisa diajukan pada posisi migration sekarang.')
  }
  m.locked_at ||= now()
  const r = {
    id: String(state.nextRequest++), migration_id: m.id, revision: m.revision, operation: body.operation, review_status: 'draft', requested_by: ME,
    target_execution_id: body.target_execution_id || null, source_job_id: body.source_job_id ? String(body.source_job_id) : null,
    snapshot: snapshotOf(m), created_at: now(), updated_at: now(), submitted_at: null, approvals: [],
  }
  state.requests.push(r)
  return send(res, 201, requestSummary(r))
}

function requestAction(res, r, action) {
  const allowed = requestDetail(r).allowed_actions
  if (action === 'execute' && r.job_id) return send(res, 202, jobDto(state.jobs.get(r.job_id))) // klik dobel -> job yang sama
  if (!allowed.includes(action)) {
    if (action === 'execute' && r.approvals.some((a) => !a.approved_by)) {
      return fail(res, 409, 'approval_incomplete', 'Masih menunggu persetujuan group.', { pending_group_ids: requestDetail(r).pending_group_ids })
    }
    return fail(res, 409, 'state_conflict', 'Status atau revisi pengajuan tidak sesuai.')
  }
  r.updated_at = now()
  if (action === 'submit') {
    r.review_status = 'pending_review'
    r.submitted_at = now()
    r.approvals = [{ group_id: '2', group_name: 'developer', approved_by: null, approved_at: null, waiting_for_leader: false, can_approve: true }]
    return send(res, 200, requestSummary(r))
  }
  if (action === 'approve') {
    for (const a of r.approvals) if (a.can_approve && !a.approved_by) Object.assign(a, { approved_by: ME, approved_at: now(), can_approve: false })
    if (r.approvals.every((a) => a.approved_by)) r.review_status = 'approved'
    return send(res, 200, { request_id: r.id, review_status: r.review_status, approved_group_ids: r.approvals.filter((a) => a.approved_by).map((a) => a.group_id) })
  }
  if (action === 'cancel') {
    r.review_status = 'cancelled'
    return send(res, 200, requestSummary(r))
  }
  const m = state.migrations.find((x) => x.id === r.migration_id)
  const source = r.source_job_id && state.jobs.get(r.source_job_id)
  const j = {
    id: String(state.nextJob++), request_id: r.id, database_id: '1', migration_id: r.migration_id, revision: r.revision, operation: r.operation,
    status: 'queued', attempt_count: 0, error: null, execution_actor_id: ME, queued_at: now(), finished_at: null, next_attempt_at: now(), compensation_job_id: null,
    progress: { total_rows: m.mode === 'batch' ? (source ? source.progress.committed_rows : 10000) : null, committed_rows: 0, committed_batches: 0, reverted_rows: 0, reverted_batches: 0, last_cursor: null },
    target_execution_id: r.target_execution_id, startMs: Date.now(), baseBatches: 0,
  }
  if (source) source.compensation_job_id = j.id
  state.jobs.set(j.id, j)
  r.job_id = j.id
  return send(res, 202, jobDto(j))
}

// ---------- job (kontrak 9.6) ----------

// simulasi worker: job maju sendiri tiap kali dibaca. Normal selesai ~4 detik, batch 1 batch/0.7 detik
function advance(j) {
  if (!j.startMs || !ACTIVE.includes(j.status)) return
  const t = Date.now() - j.startMs
  if (t < 1500) return
  if (j.status === 'queued') Object.assign(j, { status: 'running', attempt_count: j.attempt_count + 1 })
  const p = j.progress
  if (p.total_rows !== null) {
    const total = Math.ceil(p.total_rows / 1000)
    const done = Math.min(total, j.baseBatches + Math.floor((t - 1500) / 700))
    Object.assign(p, { committed_batches: done, committed_rows: Math.min(p.total_rows, done * 1000), last_cursor: String(done * 1000) })
    if (done === total) finish(j)
  } else if (t > 4000) finish(j)
}

function finish(j) {
  Object.assign(j, { status: 'success', finished_at: now(), error: null })
  const m = state.migrations.find((x) => x.id === j.migration_id)
  const seq = state.executions.filter((e) => e.history_sequence).length + 1
  state.executions.push(
    execution(state.nextExecution++, j.migration_id, j.operation, 'success', 0, {
      job_id: j.id, request_id: j.request_id, attempt: j.attempt_count, target_execution_id: j.target_execution_id || null,
      history_sequence: String(seq), started_at: j.queued_at, finished_at: now(), progress: { ...j.progress },
      sql_snapshot: ['rollback', 'compensate'].includes(j.operation) ? m.sql_down : m.sql_up,
    }),
  )
  if (j.operation === 'compensate') {
    const source = [...state.jobs.values()].find((x) => x.compensation_job_id === j.id)
    if (source) Object.assign(source, { status: 'compensated', progress: { ...source.progress, reverted_rows: source.progress.committed_rows, reverted_batches: source.progress.committed_batches } })
  }
}

function jobDto(j) {
  advance(j)
  const compensationRequested = state.requests.some((r) => r.source_job_id === j.id && r.review_status !== 'cancelled')
  const actions = []
  if (['partial', 'partially_reverted'].includes(j.status) && !j.compensation_job_id) actions.push('resume')
  if (j.status === 'partial' && !compensationRequested) actions.push('request_compensate')
  const { startMs: _s, baseBatches: _b, target_execution_id: _t, ...rest } = j
  return { ...rest, blocking_database: BLOCKING.includes(j.status), allowed_actions: actions, status_url: `/api/databases/1/jobs/${j.id}` }
}

function resumeJob(res, j) {
  if (!jobDto(j).allowed_actions.includes('resume')) return fail(res, 409, 'state_conflict', 'Job ini tidak bisa dilanjutkan.')
  Object.assign(j, { status: 'queued', error: null, startMs: Date.now(), baseBatches: j.progress.committed_batches })
  return send(res, 202, jobDto(j))
}

// ---------- router ----------

const ROUTES = [
  [/^\/api\/databases\/\d+\/migrations$/, (req, res, url, body) => {
    if (req.method === 'GET') return page(res, url, [...state.migrations].sort((a, b) => b.updated_at.localeCompare(a.updated_at)).map(migrationDto))
    const field = invalidMigration(body)
    if (field) return fail(res, 400, 'invalid_input', `Field ${field} belum valid.`, { field })
    const m = { ...migration(state.nextMigration++, body.title), ...body, created_at: now(), updated_at: now(), locked_at: null }
    state.migrations.push(m)
    return send(res, 201, migrationDto(m))
  }],
  [/^\/api\/databases\/\d+\/migrations\/(\d+)$/, (req, res, url, body, [id]) => {
    const m = state.migrations.find((x) => x.id === id)
    if (!m) return fail(res, 404, 'not_found', 'Migration tidak ditemukan.')
    if (req.method === 'GET') return send(res, 200, migrationDto(m))
    if (m.locked_at) return fail(res, 409, 'immutable_migration', 'Migration sudah terkunci dan tidak dapat diubah.')
    if (body.revision !== m.revision) return fail(res, 409, 'state_conflict', 'Revisi migration sudah berubah.')
    const field = invalidMigration({ ...m, ...body })
    if (field) return fail(res, 400, 'invalid_input', `Field ${field} belum valid.`, { field })
    const { revision: _r, ...changes } = body
    Object.assign(m, changes, { updated_at: now() })
    return send(res, 200, migrationDto(m))
  }],
  [/^\/api\/databases\/\d+\/requests$/, (req, res, url, body) => {
    if (req.method === 'POST') return createRequest(res, body)
    return page(res, url, [...state.requests].sort((a, b) => b.created_at.localeCompare(a.created_at)).map(requestSummary))
  }],
  [/^\/api\/databases\/\d+\/requests\/(\d+)(?:\/(submit|approve|cancel|execute))?$/, (req, res, url, body, [id, action]) => {
    const r = state.requests.find((x) => x.id === id)
    if (!r) return fail(res, 404, 'not_found', 'Pengajuan atau migration tidak ditemukan.')
    if (req.method === 'GET' && !action) return send(res, 200, requestDetail(r))
    if (req.method !== 'POST' || !action) return fail(res, 404, 'not_found', 'Resource tidak ditemukan.')
    return requestAction(res, r, action)
  }],
  [/^\/api\/databases\/\d+\/jobs\/(\d+)(\/resume)?$/, (req, res, url, body, [id, resume]) => {
    const j = state.jobs.get(id)
    if (!j) return fail(res, 404, 'not_found', 'Job tidak ditemukan.')
    if (req.method === 'POST' && resume) return resumeJob(res, j)
    return send(res, 200, jobDto(j))
  }],
  [/^\/api\/databases\/\d+\/history$/, (req, res, url) => {
    for (const j of state.jobs.values()) advance(j)
    const all = state.executions.map(({ batch_attempts: _b, ...e }) => ({ ...e, migration_title: state.migrations.find((m) => m.id === e.migration_id)?.title }))
    return page(res, url, url.searchParams.get('hide_rollback') === 'true' ? hideRollback(all) : all)
  }],
  [/^\/api\/databases\/\d+\/executions\/(\d+)$/, (req, res, url, body, [id]) => {
    const e = state.executions.find((x) => x.id === id)
    if (!e) return fail(res, 404, 'not_found', 'Eksekusi tidak ditemukan.')
    return send(res, 200, { ...e, migration_title: state.migrations.find((m) => m.id === e.migration_id)?.title })
  }],
]

function readBody(req) {
  return new Promise((resolve) => {
    let raw = ''
    req.on('data', (chunk) => (raw += chunk))
    req.on('end', () => {
      try {
        resolve(raw ? JSON.parse(raw) : {})
      } catch {
        resolve({})
      }
    })
  })
}

function handle(req, res, next) {
  const url = new URL(req.url, 'http://mock')
  const path = url.pathname.replace(/\/$/, '')
  if (path === '/api/__mock/reset' && req.method === 'POST') {
    state = seed()
    return send(res, 200, null)
  }
  const route = ROUTES.find(([pattern]) => pattern.test(path))
  if (!route) return next()
  const params = path.match(route[0]).slice(1)
  // jeda kecil biar state loading/tombol terkunci kelihatan
  readBody(req).then((body) => setTimeout(() => route[1](req, res, url, body, params), 250))
}

export function mockApi() {
  return {
    name: 'tubes-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use(handle)
      server.config.logger.info('  ➜  Mock API aktif untuk migration, request, job, dan history')
    },
  }
}
