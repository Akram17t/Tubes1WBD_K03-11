import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// dev: /api diterusin ke Go. Host header sengaja ga diubah (changeOrigin false)
// biar cek Origin waktu login tetap cocok tanpa matiin CSRF
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': { target: process.env.API_TARGET || 'http://localhost:8080', changeOrigin: false },
    },
  },
  test: { environment: 'node' },
})
