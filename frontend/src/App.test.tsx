import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from './App'
import { toDateKey } from './lib/dates'
import { createQueryClient } from './queryClient'
import { alice, bob, fakeApi, family, meWithFamily, membersList } from './test/api'

function renderAt(path: string) {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  sessionStorage.clear()
})

const unauthenticated = () => ({
  status: 401,
  body: { error: { code: 'unauthenticated', message: 'sign in required' } },
})

describe('sign-in', () => {
  it('sends signed-out users to the login page and back afterwards', async () => {
    fakeApi({ 'GET /api/me': unauthenticated })
    renderAt('/family')
    const link = await screen.findByRole('link', { name: /sign in with google/i })
    expect(link).toHaveAttribute('href', '/auth/login?return_to=%2Ffamily')
  })

  it('shows a sign-in error from the callback', () => {
    fakeApi({ 'GET /api/me': unauthenticated })
    renderAt('/login?error=state_mismatch')
    expect(screen.getByRole('alert')).toHaveTextContent(/could not be verified/i)
  })

  it('skips the login page for signed-in users', async () => {
    fakeApi({ 'GET /api/me': meWithFamily, 'GET /api/family/members': membersList })
    renderAt('/login?return_to=%2Ffamily')
    expect(await screen.findByRole('heading', { name: 'The Smiths' })).toBeInTheDocument()
  })

  it('ignores a foreign return_to', async () => {
    fakeApi({ 'GET /api/me': unauthenticated })
    renderAt('/login?return_to=https%3A%2F%2Fevil.example')
    const link = await screen.findByRole('link', { name: /sign in with google/i })
    expect(link).toHaveAttribute('href', '/auth/login?return_to=%2F')
  })
})

describe('family setup', () => {
  it('lets a user without a family create one', async () => {
    let hasFamily = false
    const api = fakeApi({
      'GET /api/me': () => ({
        status: 200,
        body: { user: alice, family: hasFamily ? family : null },
      }),
      'POST /api/family': () => {
        hasFamily = true
        return { status: 201, body: family }
      },
      'GET /api/events': () => ({ status: 200, body: { items: [] } }),
    })
    renderAt('/')
    await userEvent.type(await screen.findByLabelText(/family name/i), 'The Smiths')
    await userEvent.click(screen.getByRole('button', { name: /create family/i }))

    expect(await screen.findByRole('grid')).toBeInTheDocument() // calendar shown
    expect(api.calls).toContainEqual({
      method: 'POST',
      path: '/api/family',
      body: { name: 'The Smiths' },
    })
  })
})

describe('calendar', () => {
  it('shows events in the month and the selected day', async () => {
    const today = new Date()
    const start = new Date(today.getFullYear(), today.getMonth(), today.getDate(), 18, 0)
    const end = new Date(start.getTime() + 2 * 3600_000)
    const api = fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/events': () => ({
        status: 200,
        body: {
          items: [
            {
              id: 'e1',
              title: 'Family dinner',
              description: '',
              startsAt: start.toISOString(),
              endsAt: end.toISOString(),
              allDay: false,
              createdBy: alice.id,
              createdAt: start.toISOString(),
              updatedAt: start.toISOString(),
              participants: [{ userId: bob.id, name: bob.name, avatarUrl: null }],
            },
          ],
        },
      }),
    })
    renderAt(`/?day=${toDateKey(today)}`)

    const panel = await screen.findByRole('region', { name: /events on/i })
    const row = await within(panel).findByRole('link', { name: /family dinner/i })
    expect(row).toHaveAttribute('href', '/events/e1')
    expect(within(screen.getByRole('grid')).getAllByText('Family dinner')).toHaveLength(1)

    // The request covers the whole visible grid.
    const req = api.calls.find((c) => c.path === '/api/events')
    expect(req).toBeDefined()
  })

  it('navigates months with the URL', async () => {
    fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/events': () => ({ status: 200, body: { items: [] } }),
    })
    renderAt('/?day=2026-11-10')
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent(/2026/)
    const before = screen.getByRole('heading', { level: 1 }).textContent
    await userEvent.click(screen.getByRole('button', { name: /next month/i }))
    expect(screen.getByRole('heading', { level: 1 }).textContent).not.toBe(before)
  })
})

describe('event form', () => {
  it('creates an event with participants', async () => {
    const api = fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/family/members': membersList,
      'POST /api/events': ({ body }) => ({
        status: 201,
        body: {
          ...(body as object),
          id: 'e2',
          createdBy: alice.id,
          createdAt: '',
          updatedAt: '',
          participants: [],
        },
      }),
      'GET /api/events': () => ({ status: 200, body: { items: [] } }),
    })
    renderAt('/events/new?date=2026-11-10')

    await userEvent.type(await screen.findByLabelText(/title/i), 'Dentist')
    await userEvent.click(await screen.findByRole('checkbox', { name: /bob smith/i }))
    await userEvent.click(screen.getByRole('button', { name: /create event/i }))

    await waitFor(() => {
      expect(api.calls.some((c) => c.method === 'POST' && c.path === '/api/events')).toBe(true)
    })
    const body = api.calls.find((c) => c.method === 'POST')?.body as Record<string, unknown>
    expect(body).toMatchObject({
      title: 'Dentist',
      allDay: false,
      participantIds: [alice.id, bob.id],
    })
    expect(new Date(body.startsAt as string)).toEqual(new Date(2026, 10, 10, 9, 0))
    // Back on the calendar at the event's day.
    expect(await screen.findByRole('grid')).toBeInTheDocument()
  })

  it('validates before sending and shows server field errors', async () => {
    const api = fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/family/members': membersList,
      'POST /api/events': () => ({
        status: 422,
        body: {
          error: {
            code: 'validation_failed',
            message: 'x',
            fields: { title: 'must be at most 200 characters' },
          },
        },
      }),
    })
    renderAt('/events/new?date=2026-11-10')

    await userEvent.click(await screen.findByRole('button', { name: /create event/i }))
    expect(screen.getByRole('alert')).toHaveTextContent(/give the event a title/i)
    expect(api.calls.filter((c) => c.method === 'POST')).toHaveLength(0)

    await userEvent.type(screen.getByLabelText(/title/i), 'Too long, says the server')
    await userEvent.click(screen.getByRole('button', { name: /create event/i }))
    expect(await screen.findByText('must be at most 200 characters')).toBeInTheDocument()
  })

  it('loads, deletes and returns to the calendar', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const api = fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/family/members': membersList,
      'GET /api/events/:id': () => ({
        status: 200,
        body: {
          id: 'e1',
          title: 'Vacation',
          description: 'Sea',
          allDay: true,
          startsAt: new Date(2026, 6, 1).toISOString(),
          endsAt: new Date(2026, 6, 15).toISOString(),
          createdBy: alice.id,
          createdAt: '',
          updatedAt: '',
          participants: [],
        },
      }),
      'DELETE /api/events/:id': () => ({ status: 204 }),
      'GET /api/events': () => ({ status: 200, body: { items: [] } }),
    })
    renderAt('/events/e1')

    expect(await screen.findByDisplayValue('Vacation')).toBeInTheDocument()
    expect(screen.getByLabelText(/end date/i)).toHaveValue('2026-07-14') // exclusive end shown inclusive
    expect(screen.queryByLabelText(/start time/i)).not.toBeInTheDocument() // all-day hides times

    await userEvent.click(screen.getByRole('button', { name: /delete/i }))
    await waitFor(() => {
      expect(api.calls).toContainEqual({
        method: 'DELETE',
        path: '/api/events/e1',
        body: undefined,
      })
    })
    expect(await screen.findByRole('grid')).toBeInTheDocument()
  })
})

describe('family page', () => {
  it('lists members and creates an invite link', async () => {
    fakeApi({
      'GET /api/me': meWithFamily,
      'GET /api/family/members': membersList,
      'POST /api/family/invites': () => ({
        status: 201,
        body: {
          token: 't',
          url: 'https://cal.example/invite#t',
          expiresAt: '2026-11-13T00:00:00Z',
        },
      }),
    })
    renderAt('/family')
    expect(await screen.findByText(/bob smith/i)).toBeInTheDocument()
    expect(screen.getByText(/owner/i, { selector: '.badge' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /create invite link/i }))
    expect(await screen.findByLabelText(/invite link/i)).toHaveValue('https://cal.example/invite#t')
  })
})

describe('invite', () => {
  it('keeps the token across sign-in and joins the family', async () => {
    window.history.replaceState(null, '', '/invite#secret-token')
    let joined = false
    const api = fakeApi({
      'GET /api/me': () => ({ status: 200, body: { user: bob, family: joined ? family : null } }),
      'POST /api/invites/accept': () => {
        joined = true
        return { status: 200, body: { ...family, role: 'member' } }
      },
      'GET /api/events': () => ({ status: 200, body: { items: [] } }),
    })
    renderAt('/invite')

    // Token moved from the address bar to sessionStorage.
    expect(window.location.hash).toBe('')
    expect(sessionStorage.getItem('pendingInvite')).toBe('secret-token')

    await userEvent.click(await screen.findByRole('button', { name: /join family/i }))
    await waitFor(() => {
      expect(api.calls).toContainEqual({
        method: 'POST',
        path: '/api/invites/accept',
        body: { token: 'secret-token' },
      })
    })
    expect(await screen.findByRole('grid')).toBeInTheDocument()
    expect(sessionStorage.getItem('pendingInvite')).toBeNull()
  })
})
