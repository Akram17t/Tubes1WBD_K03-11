import { useCallback, useEffect, useRef, useState } from 'react'

// ambil data GET waktu komponen muncul / deps berubah. fetcher dapet AbortSignal
// biar request lama dibatalin kalau user udah pindah halaman
export function useApi(fetcher, deps) {
  const [state, setState] = useState({ data: null, meta: null, error: null, loading: true })
  const [tick, setTick] = useState(0)
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  useEffect(() => {
    const controller = new AbortController()
    setState((s) => ({ ...s, loading: true, error: null }))
    fetcherRef
      .current(controller.signal)
      .then((res) => !controller.signal.aborted && setState({ data: res.data, meta: res.meta, error: null, loading: false }))
      .catch((error) => {
        if (controller.signal.aborted || error?.name === 'AbortError') return
        setState((s) => ({ ...s, error, loading: false }))
      })
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  const reload = useCallback(() => setTick((t) => t + 1), [])
  return { ...state, reload }
}

// bungkus aksi mutasi: busy = tombol dikunci, error disimpen buat ditampilin
export function useAction() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  const run = useCallback(async (action) => {
    setBusy(true)
    setError(null)
    try {
      return await action()
    } catch (err) {
      setError(err)
      throw err
    } finally {
      setBusy(false)
    }
  }, [])

  return { busy, error, setError, run }
}
