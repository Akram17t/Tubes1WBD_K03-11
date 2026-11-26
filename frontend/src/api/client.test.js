import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, buildUrl, configureClient, setCsrfToken } from './client.js'

// bikin Response palsu berformat JSON kayak backend
function jsonResponse(status, payload, headers = {}) {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

let fetchMock
let onUnauthenticated
let onCsrfInvalid

beforeEach(() => {
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  onUnauthenticated = vi.fn()
  onCsrfInvalid = vi.fn()
  configureClient({ onUnauthenticated, onCsrfInvalid })
  setCsrfToken('tok-123')
})

afterEach(() => {
  vi.unstubAllGlobals()
  setCsrfToken(null)
})

describe('buildUrl', () => {
  it('nambah prefix /api dan buang query kosong', () => {
    expect(buildUrl('/databases', { limit: 50, offset: 0, q: '', x: null })).toBe('/api/databases?limit=50&offset=0')
    expect(buildUrl('/auth/me')).toBe('/api/auth/me')
  })
})

describe('request', () => {
  it('GET ga bawa token CSRF, balikin data + meta', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { data: [{ id: '1' }], meta: { request_id: 'r1', has_more: false } }))
    const res = await api.get('/databases', { limit: 50 })
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/databases?limit=50')
    expect(init.headers['X-CSRF-Token']).toBeUndefined()
    expect(init.credentials).toBe('same-origin')
    expect(res).toEqual({ data: [{ id: '1' }], meta: { request_id: 'r1', has_more: false } })
  })

  it('mutasi bawa X-CSRF-Token dan body JSON', async () => {
    fetchMock.mockResolvedValue(jsonResponse(201, { data: { id: '9' }, meta: {} }))
    await api.post('/admin/users', { username: 'budi' })
    const [, init] = fetchMock.mock.calls[0]
    expect(init.method).toBe('POST')
    expect(init.headers['X-CSRF-Token']).toBe('tok-123')
    expect(init.headers['Content-Type']).toBe('application/json')
    expect(JSON.parse(init.body)).toEqual({ username: 'budi' })
  })

  it('error dibungkus ApiError lengkap dengan code, details, request_id', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(409, {
        error: { code: 'approval_incomplete', message: 'Masih menunggu.', details: { pending_group_ids: ['2'] } },
        meta: { request_id: 'r9' },
      }),
    )
    const err = await api.post('/x', {}).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'approval_incomplete', requestId: 'r9', message: 'Masih menunggu.' })
    expect(err.details).toEqual({ pending_group_ids: ['2'] })
  })

  it('401 di luar login manggil onUnauthenticated', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'unauthenticated', message: 'Login dulu.' }, meta: {} }))
    await expect(api.get('/databases')).rejects.toMatchObject({ status: 401 })
    expect(onUnauthenticated).toHaveBeenCalledOnce()
  })

  it('401 dari login (password salah) ga dianggap session habis', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'unauthenticated', message: 'Salah.' }, meta: {} }))
    await expect(api.post('/auth/login', { username: 'a', password: 'b' })).rejects.toMatchObject({ status: 401 })
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('403 csrf_invalid refresh token sekali tapi mutasi TIDAK diulang', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { error: { code: 'csrf_invalid', message: 'x' }, meta: {} }))
    await expect(api.post('/databases/1/requests/4/execute')).rejects.toMatchObject({ code: 'csrf_invalid' })
    expect(onCsrfInvalid).toHaveBeenCalledOnce()
    expect(fetchMock).toHaveBeenCalledOnce()
  })

  it('403 forbidden biasa ga nyentuh token', async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { error: { code: 'forbidden', message: 'Akses ditolak.' }, meta: {} }))
    await expect(api.get('/admin/users')).rejects.toMatchObject({ code: 'forbidden' })
    expect(onCsrfInvalid).not.toHaveBeenCalled()
  })

  it('server mati / response bukan JSON tetap jadi ApiError yang aman', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await expect(api.get('/auth/me')).rejects.toMatchObject({ status: 0, code: 'network_error' })

    fetchMock.mockResolvedValueOnce(new Response('Bad Gateway', { status: 502, headers: { 'X-Request-ID': 'gw1' } }))
    await expect(api.get('/auth/me')).rejects.toMatchObject({ status: 502, code: 'http_502', requestId: 'gw1' })
  })
})
