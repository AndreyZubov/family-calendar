import { vi } from 'vitest'

type Handler = (req: { body: unknown; url: URL }) => { status: number; body?: unknown }

/**
 * A tiny fake backend for component tests: routes "METHOD /path" (path
 * params as :name) to handlers, and records requests for assertions.
 */
export function fakeApi(routes: Record<string, Handler>) {
  const calls: { method: string; path: string; body: unknown }[] = []
  const compiled = Object.entries(routes).map(([key, handler]) => {
    const [method, pattern] = key.split(' ') as [string, string]
    const re = new RegExp('^' + pattern.replace(/:[^/]+/g, '[^/]+') + '$')
    return { method, re, handler }
  })

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: Request) => {
      const url = new URL(input.url)
      const text = await input.text()
      const body: unknown = text ? JSON.parse(text) : undefined
      calls.push({ method: input.method, path: url.pathname, body })
      const route = compiled.find((r) => r.method === input.method && r.re.test(url.pathname))
      const res = route
        ? route.handler({ body, url })
        : { status: 404, body: { error: { code: 'not_found', message: 'no fake route' } } }
      return new Response(res.body === undefined ? null : JSON.stringify(res.body), {
        status: res.status,
        headers: { 'Content-Type': 'application/json' },
      })
    }),
  )
  return { calls }
}

export const alice = {
  id: 'u-alice',
  email: 'alice@example.com',
  name: 'Alice Smith',
  avatarUrl: null,
}
export const bob = { id: 'u-bob', email: 'bob@example.com', name: 'Bob Smith', avatarUrl: null }
export const family = { id: 'f-1', name: 'The Smiths', role: 'owner' as const }

export const meWithFamily = () => ({ status: 200, body: { user: alice, family } })

export const membersList = () => ({
  status: 200,
  body: {
    items: [
      {
        userId: alice.id,
        email: alice.email,
        name: alice.name,
        avatarUrl: null,
        role: 'owner',
        joinedAt: '2026-11-01T00:00:00Z',
      },
      {
        userId: bob.id,
        email: bob.email,
        name: bob.name,
        avatarUrl: null,
        role: 'member',
        joinedAt: '2026-11-02T00:00:00Z',
      },
    ],
  },
})
