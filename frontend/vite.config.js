import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { mockApi } from './mock/mockApi.js'

// dev: /api diterusin ke Go. Host header sengaja ga diubah (changeOrigin false)
// biar cek Origin waktu login tetap cocok tanpa matiin CSRF.
// `npm run dev:mock` (mode mock) nambahin mock buat endpoint workflow yang belum siap di backend
export default defineConfig(({ mode }) => ({
  plugins: [react(), mode === 'mock' && mockApi()],
  server: {
    proxy: {
      '/api': { target: process.env.API_TARGET || 'http://localhost:8080', changeOrigin: false },
    },
  },
  test: { environment: 'node' },
}))
