import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { api, configureClient, setCsrfToken } from '../api/client.js'

const AuthContext = createContext(null)

// status: 'loading' (cek /auth/me), 'authenticated', 'anonymous', 'error' (server ga bisa dihubungi)
export function AuthProvider({ children }) {
  const [state, setState] = useState({ status: 'loading', user: null, expired: false, error: null })

  // simpen user + token CSRF terbaru dari response login/me
  const applyUser = useCallback((user) => {
    setCsrfToken(user.csrf_token)
    setState({ status: 'authenticated', user, expired: false, error: null })
  }, [])

  // session habis di tengah jalan: hapus user, login page nampilin info "sesi berakhir"
  const clearUser = useCallback((expired) => {
    setCsrfToken(null)
    setState((prev) => ({ status: 'anonymous', user: null, expired: expired && prev.status === 'authenticated', error: null }))
  }, [])

  // ambil ulang user + token dari server, dipakai waktu refresh halaman dan token CSRF basi
  const refresh = useCallback(async () => {
    try {
      const { data } = await api.get('/auth/me')
      applyUser(data)
    } catch (err) {
      if (err.status === 401) clearUser(false)
      else setState({ status: 'error', user: null, expired: false, error: err })
    }
  }, [applyUser, clearUser])

  useEffect(() => {
    configureClient({ onUnauthenticated: () => clearUser(true), onCsrfInvalid: refresh })
    refresh()
  }, [clearUser, refresh])

  const login = useCallback(
    async (username, password) => {
      const { data } = await api.post('/auth/login', { username, password })
      applyUser(data)
      return data
    },
    [applyUser],
  )

  // session di server dicabut dulu; kalau gagal pun state lokal tetap dibersihin
  const logout = useCallback(async () => {
    try {
      await api.post('/auth/logout')
    } finally {
      clearUser(false)
    }
  }, [clearUser])

  const value = useMemo(() => ({ ...state, login, logout, refresh }), [state, login, logout, refresh])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth harus dipakai di dalam AuthProvider')
  return ctx
}
