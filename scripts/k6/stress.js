import http from 'k6/http'
import exec from 'k6/execution'
import { check, fail, sleep } from 'k6'
import { Counter, Rate, Trend } from 'k6/metrics'

// Pakai dua target test berisi load_counter (id BIGINT PRIMARY KEY, value BIGINT), row awal (1,0).
// Seed editor/viewer harus menjadi leader developer/analyst; beri kedua group akses ke dua target.
// Jalankan API dan worker, lalu: k6 run -e K6_DATABASE_IDS=2,3 scripts/k6/stress.js
const base = __ENV.K6_BASE_URL || 'http://127.0.0.1:18080'
const databases = (__ENV.K6_DATABASE_IDS || '2,3').split(',')
const phases = [['normal', 2], ['elevated', 8], ['peak', 24], ['recovery', 2]]
const latency = new Trend('api_latency_ms', true)
const errors = new Rate('api_error_rate')
const requests = new Counter('api_requests')
const completed = new Counter('jobs_completed')
const success = new Rate('job_success_rate')
const queued = new Trend('queue_wait_ms', true)
const duration = new Trend('job_duration_ms', true)
const elapsed = new Trend('phase_elapsed_ms', true)
let actor

const thresholds = {
  api_latency_ms: ['p(95)<1000'], api_error_rate: ['rate<0.01'],
  job_success_rate: ['rate>0.99'], checks: ['rate==1'],
}
for (const [phase] of phases) {
  for (const metric of ['api_latency_ms', 'queue_wait_ms', 'job_duration_ms', 'phase_elapsed_ms']) {
    thresholds[`${metric}{phase:${phase}}`] = ['p(95)>=0']
  }
  thresholds[`api_requests{phase:${phase}}`] = ['count>0']
  thresholds[`jobs_completed{phase:${phase}}`] = ['count>0']
  thresholds[`api_error_rate{phase:${phase}}`] = ['rate<0.01']
}
export const options = {
  setupTimeout: '5m', noCookiesReset: true,
  scenarios: Object.fromEntries(phases.map(([phase, vus], i) => [phase, {
    executor: 'constant-vus', vus, duration: '20s', startTime: `${i * 35}s`, gracefulStop: '15s',
  }])),
  thresholds, summaryTrendStats: ['med', 'p(95)', 'max'],
  systemTags: ['status', 'method', 'name', 'scenario', 'expected_response'],
}

// request menghentikan HTTP error; persiapan dan login tidak masuk metric API pengukuran.
function request(method, path, body, user, phase, name, status = 200) {
  const response = http.request(method, `${base}/api${path}`, body === null ? null : JSON.stringify(body), {
    jar: user.jar, headers: { 'Content-Type': 'application/json', Origin: base, 'X-CSRF-Token': user.csrf || '' },
    tags: { name }, timeout: '30s',
  })
  if (phase !== 'setup' && name !== 'login') {
    latency.add(response.timings.duration, { phase })
    errors.add(response.status !== status, { phase })
    requests.add(1, { phase })
  }
  if (!check(response, { 'HTTP sesuai': (r) => r.status === status })) {
    sleep(0.5)
    fail(`${name}: HTTP ${response.status}`)
  }
  return response.json('data')
}

function login(username, phase) {
  const user = { jar: new http.CookieJar() }
  user.csrf = request('POST', '/auth/login', { username, password: __ENV.K6_PASSWORD || 'demo12345' }, user, phase, 'login').csrf_token
  return user
}

// setup menyiapkan seluruh request approved sebelum periode beban dimulai.
export function setup() {
  if (databases.length !== 2 || new Set(databases).size !== 2 || databases.some((id) => !/^[1-9]\d*$/.test(id))) fail('Isi K6_DATABASE_IDS dengan dua ID target test berbeda.')
  const editor = login('editor', 'setup'), viewer = login('viewer', 'setup')
  const fixture = {}
  const post = (path, body, user = editor, status = 200) => request('POST', path, body, user, 'setup', 'prepare', status)
  for (const [phase, vus] of phases) {
    fixture[phase] = []
    // Think time 500 ms membatasi pool ke 40 iterasi per VU, ditambah dua cadangan.
    for (let i = 0; i < vus * 42; i++) {
      const database = databases[i % 2], path = `/databases/${database}`
      const migration = post(`${path}/migrations`, { title: `k6 ${phase} ${i}`, mode: 'normal', irreversible: false,
        sql_up: 'UPDATE public.load_counter SET value=value+1 WHERE id=1',
        sql_down: 'UPDATE public.load_counter SET value=value-1 WHERE id=1' }, editor, 201)
      const entry = post(`${path}/requests`, { migration_id: migration.id, revision: migration.revision, operation: 'up' }, editor, 201)
      const review = `${path}/requests/${entry.id}`
      post(`${review}/submit`, {})
      post(`${review}/approve`, {})
      if (post(`${review}/approve`, {}, viewer).review_status !== 'approved') fail('Approval belum lengkap.')
      fixture[phase].push({ database, request: entry.id })
    }
  }
  return fixture
}

export default function (fixture) {
  const phase = exec.scenario.name
  actor ||= login('editor', phase)
  const entry = fixture[phase][exec.scenario.iterationInTest]
  if (!entry) { sleep(0.5); fail('Pool request approved habis.') }
  const path = `/databases/${entry.database}`
  const call = (method, url, body, name, status = 200) => request(method, url, body, actor, phase, name, status)
  call('GET', '/databases', null, 'databases')
  call('GET', `${path}/history?limit=10`, null, 'history')
  const job = call('POST', `${path}/requests/${entry.request}/execute`, {}, 'enqueue', 202)
  const duplicate = call('POST', `${path}/requests/${entry.request}/execute`, {}, 'duplicate_enqueue', 202)
  check(duplicate, { 'Job duplikat sama': (j) => j.id === job.id })
  const deadline = Date.now() + 14000
  let result
  do {
    sleep(0.1)
    result = call('GET', `${path}/jobs/${job.id}`, null, 'job_status')
  } while (['queued', 'running'].includes(result.status) && Date.now() < deadline)
  success.add(result.status === 'success', { phase })
  check(result, { 'Worker selesai sukses': (j) => j.status === 'success' })
  if (result.status === 'success') {
    completed.add(1, { phase })
    queued.add(Date.parse(result.started_at) - Date.parse(result.queued_at), { phase })
    duration.add(Date.parse(result.finished_at) - Date.parse(result.started_at), { phase })
  }
  elapsed.add(Date.now() - exec.scenario.startTime, { phase })
  sleep(0.5)
}

// Summary menyimpan hasil per fase; throughput memakai durasi fase beserta drain, bukan seluruh run.
export function handleSummary(data) {
  const value = (metric, phase) => data.metrics[`${metric}{phase:${phase}}`]?.values
  let report = '# Hasil k6\n\nBeban 2 → 8 → 24 → 2 VU, masing-masing 20 detik + drain 15 detik. Setup/approval dan login dikecualikan dari metric API.\n\n'
  report += '| Fase | VU | API p50/p95 ms | Request/s | Error | Job sukses | Job/s | Queue p95 ms | Job p95 ms |\n|---|---|---|---|---|---|---|---|---|\n'
  for (const [phase, vus] of phases) {
    const a = value('api_latency_ms', phase), r = value('api_requests', phase), jobs = value('jobs_completed', phase)
    if (!a || !r || !jobs) { report += `| ${phase} | ${vus} | Fase tidak lengkap |\n`; continue }
    const seconds = Math.max(20, value('phase_elapsed_ms', phase).max / 1000)
    report += `| ${phase} | ${vus} | ${a.med.toFixed(2)}/${a['p(95)'].toFixed(2)} | ${(r.count / seconds).toFixed(2)} | ${(value('api_error_rate', phase).rate * 100).toFixed(2)}% | ${jobs.count} | ${(jobs.count / seconds).toFixed(2)} | ${value('queue_wait_ms', phase)['p(95)'].toFixed(2)} | ${value('job_duration_ms', phase)['p(95)'].toFixed(2)} |\n`
  }
  const failed = Object.entries(data.metrics).flatMap(([name, m]) => Object.entries(m.thresholds || {}).filter(([, t]) => !t.ok).map(([rule]) => `${name}: ${rule}`))
  report += `\nThreshold gagal: ${failed.length ? failed.join(', ') : 'tidak ada'}.\n`
  return { stdout: report }
}
