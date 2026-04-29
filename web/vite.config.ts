import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'

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
        target: 'http://10.11.3.190:8080',
        changeOrigin: true,
        ws: true,
        cookieDomainRewrite: 'localhost',
        secure: false,
      },
      '/healthz': {
        target: 'http://10.11.3.190:8080',
        changeOrigin: true,
      },
    },
  },
})
