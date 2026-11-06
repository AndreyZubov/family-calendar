import { randomBytes } from 'node:crypto'

import { defineConfig, devices } from '@playwright/test'

// End-to-end tests run the whole stack on their own ports, so they do not
// clash with a running `make dev`:
//
//   browser → Vite :5174 → Go backend :8081 → Postgres (E2E_DATABASE_URL)
//                              ↘ dev OIDC provider :9091 (instead of Google)
//
// Run with `make e2e`: it builds the Go binaries and creates a fresh database.
const ports = { app: 5174, backend: 8081, oidc: 9091 }
const databaseURL = process.env.E2E_DATABASE_URL
if (!databaseURL) throw new Error('E2E_DATABASE_URL is not set; run the tests with `make e2e`')

const backendEnv = {
  APP_ENV: 'development',
  HTTP_ADDR: `127.0.0.1:${String(ports.backend)}`,
  APP_BASE_URL: `http://localhost:${String(ports.app)}`,
  LOG_LEVEL: 'warn',
  DATABASE_URL: databaseURL,
  OIDC_ISSUER_URL: `http://localhost:${String(ports.oidc)}`,
  OIDC_CLIENT_ID: 'dev',
  OIDC_CLIENT_SECRET: 'dev',
  SESSION_SECRET: randomBytes(32).toString('base64'),
}

export default defineConfig({
  testDir: './e2e',
  // Scenarios share one database and sign in as different people; running
  // them one by one keeps them deterministic.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://localhost:${String(ports.app)}`,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: '../bin/devoidc',
      url: `http://localhost:${String(ports.oidc)}/.well-known/openid-configuration`,
      env: { DEV_OIDC_PORT: String(ports.oidc) },
      reuseExistingServer: false,
    },
    {
      // The backend discovers the provider on startup, so wait for it first.
      command: `until curl -sf http://localhost:${String(ports.oidc)}/.well-known/openid-configuration >/dev/null; do sleep 0.2; done; exec ../bin/server`,
      url: `http://localhost:${String(ports.backend)}/readyz`,
      env: backendEnv,
      reuseExistingServer: false,
      stdout: 'pipe',
    },
    {
      command: `npx --no-install vite --port ${String(ports.app)} --strictPort`,
      url: `http://localhost:${String(ports.app)}`,
      env: { BACKEND_URL: `http://localhost:${String(ports.backend)}` },
      reuseExistingServer: false,
    },
  ],
})
