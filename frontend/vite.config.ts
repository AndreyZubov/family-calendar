import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// The Go backend runs on :8080. In development the browser only talks to
// Vite (:5173), which forwards API and sign-in requests to Go. To the
// browser everything is one origin, so cookies and the OIDC redirect behave
// exactly as in production, where Go serves the built app itself.
const backend = 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: backend },
      '/auth': { target: backend },
    },
  },
  build: {
    outDir: 'dist',
    // Never inline assets as data: URIs into JS/CSS; keeps the CSP simple.
    assetsInlineLimit: 0,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
