import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

// In Docker `npm run dev` is not used — nginx handles proxying.
// Locally, point at the controller directly. Override with VITE_API_HOST env var.
const apiTarget = process.env.VITE_API_HOST ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  base: '/',
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/v1': {
        target: apiTarget,
        changeOrigin: true,
        ws: true,
        secure: false,
      },
      '/healthz': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
})
