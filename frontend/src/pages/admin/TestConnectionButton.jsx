import { useState } from 'react'
import { api } from '../../api/client.js'

// uji koneksi tersimpan di server; hasilnya cuma berhasil/gagal + pesan aman (tanpa DSN)
export function TestConnectionButton({ connectionId, disabled }) {
  const [state, setState] = useState({ busy: false, result: null })

  async function handleTest() {
    setState({ busy: true, result: null })
    try {
      await api.post(`/admin/connections/${connectionId}/test`)
      setState({ busy: false, result: { ok: true, text: 'Terhubung' } })
    } catch (err) {
      setState({ busy: false, result: { ok: false, text: err.message } })
    }
  }

  return (
    <span className="d-inline-flex align-items-center gap-2">
      {state.result && (
        <span className={'small ' + (state.result.ok ? 'text-success' : 'text-danger')} role="status">
          {state.result.ok ? '✓ ' : '✗ '}
          {state.result.text}
        </span>
      )}
      <button type="button" className="btn btn-sm btn-outline-secondary" disabled={disabled || state.busy} onClick={handleTest}>
        {state.busy ? 'Menguji...' : 'Uji koneksi'}
      </button>
    </span>
  )
}
