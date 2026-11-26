// Satu-satunya pintu SPA ke backend. Semua request lewat /api relatif,
// mutasi otomatis bawa header CSRF, error dibungkus jadi ApiError.

const SAFE_METHODS = new Set(['GET', 'HEAD'])
const LOGIN_PATH = '/auth/login'

// error dari API, isinya sama kayak body error kontrak + status HTTP dan request_id
export class ApiError extends Error {
  constructor(status, code, message, details = null, requestId = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
    this.requestId = requestId
  }
}

let csrfToken = null
let hooks = { onUnauthenticated: null, onCsrfInvalid: null }

// token dari /auth/login atau /auth/me, null waktu logout
export function setCsrfToken(token) {
  csrfToken = token || null
}

// AuthContext daftarin apa yang harus dilakuin waktu session habis / token CSRF basi
export function configureClient(next) {
  hooks = { ...hooks, ...next }
}

// gabungin path + query, nilai null/undefined/'' dibuang
export function buildUrl(path, query) {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query || {})) {
    if (value !== null && value !== undefined && value !== '') params.append(key, String(value))
  }
  const qs = params.toString()
  return '/api' + path + (qs ? '?' + qs : '')
}

// kirim request dan balikin { data, meta }; gagal -> lempar ApiError.
// Mutasi yang ketolak CSRF TIDAK diulang otomatis biar execute ga kejadian dobel
export async function request(method, path, { body, query, signal } = {}) {
  const headers = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (!SAFE_METHODS.has(method) && csrfToken) headers['X-CSRF-Token'] = csrfToken

  let response
  try {
    response = await fetch(buildUrl(path, query), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
      signal,
    })
  } catch (err) {
    if (err?.name === 'AbortError') throw err
    throw new ApiError(0, 'network_error', 'Tidak bisa terhubung ke server. Cek koneksi lalu coba lagi.')
  }

  const payload = await readJson(response)
  const requestId = payload?.meta?.request_id || response.headers.get('X-Request-ID')
  if (response.ok) return { data: payload?.data ?? null, meta: payload?.meta ?? {} }

  const error = new ApiError(
    response.status,
    payload?.error?.code || `http_${response.status}`,
    payload?.error?.message || 'Terjadi kesalahan pada server.',
    payload?.error?.details || null,
    requestId,
  )
  // login salah juga 401, tapi itu bukan berarti session habis
  if (response.status === 401 && path !== LOGIN_PATH) hooks.onUnauthenticated?.()
  if (response.status === 403 && error.code === 'csrf_invalid') {
    await hooks.onCsrfInvalid?.()
    error.message = 'Sesi diperbarui. Silakan ulangi aksi tadi.'
  }
  throw error
}

// body non-JSON (misal proxy mati) dianggap kosong
async function readJson(response) {
  if (!(response.headers.get('Content-Type') || '').includes('application/json')) return null
  try {
    return await response.json()
  } catch {
    return null
  }
}

export const api = {
  get: (path, query, options) => request('GET', path, { ...options, query }),
  post: (path, body, options) => request('POST', path, { ...options, body }),
  patch: (path, body, options) => request('PATCH', path, { ...options, body }),
  put: (path, body, options) => request('PUT', path, { ...options, body }),
  del: (path, options) => request('DELETE', path, options),
}
