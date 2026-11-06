import createClient from 'openapi-fetch'

import type { components, paths } from './schema'

// Types generated from backend/api/openapi.yaml (`npm run generate:api`).
export type Schemas = components['schemas']
export type Me = Schemas['Me']
export type User = Schemas['User']
export type Family = Schemas['Family']
export type CalendarEvent = Schemas['Event']
export type EventInput = Schemas['EventInput']
export type FamilyMember = Schemas['FamilyMember']
type ErrorBody = Schemas['Error']

/**
 * Typed API client. Paths, parameters, bodies and responses are checked
 * against the OpenAPI spec at compile time: calling an endpoint that does not
 * exist, or reading a field the API does not return, is a type error.
 *
 * Requests go to the same origin (Vite proxies /api in development), and the
 * HttpOnly session cookie is sent automatically by the browser.
 */
export const api = createClient<paths>({
  // Absolute same-origin URLs: `new Request()` outside the browser (tests)
  // rejects relative ones.
  baseUrl: globalThis.location.origin,
  credentials: 'same-origin',
  headers: { Accept: 'application/json' },
  // Resolve fetch on every call instead of capturing it at import time, so
  // tests can stub it.
  fetch: (request) => globalThis.fetch(request),
})

/** An error response from the API, with the stable `code` from the server. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>

  constructor(status: number, body: ErrorBody | undefined) {
    super(body?.error.message ?? `HTTP ${String(status)}`)
    this.name = 'ApiError'
    this.status = status
    this.code = body?.error.code ?? 'unknown'
    this.fields = body?.error.fields ?? {}
  }
}

/**
 * Turns an openapi-fetch result into data or a thrown ApiError, which is the
 * shape TanStack Query expects from query functions.
 */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    throw new ApiError(result.response.status, result.error as ErrorBody | undefined)
  }
  return result.data as T
}

export function isApiError(err: unknown, code?: string): err is ApiError {
  return err instanceof ApiError && (code === undefined || err.code === code)
}
