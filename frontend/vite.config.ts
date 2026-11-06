import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// The Go backend runs on :8080. In development the browser only talks to
// Vite (:5173), which forwards API and sign-in requests to Go. To the
// browser everything is one origin, so cookies and the OIDC redirect behave
// exactly as in production, where Go serves the built app itself.
// BACKEND_URL lets end-to-end tests point Vite at their own backend.
const backend = process.env.BACKEND_URL ?? 'http://localhost:8080'

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
    include: ['src/**/*.test.{ts,tsx}'], // e2e/ is Playwright's
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
