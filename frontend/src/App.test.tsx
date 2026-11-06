import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { App } from './App'
import { createQueryClient } from './queryClient'

function renderAt(path: string) {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <MemoryRouter initialEntries={[path]}>
        <App />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

function mockFetch(status: number, body: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    ),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('App', () => {
  it('sends signed-out users to the login page', async () => {
    mockFetch(401, { error: { code: 'unauthenticated', message: 'sign in required' } })
    renderAt('/')
    const link = await screen.findByRole('link', { name: /sign in with google/i })
    expect(link).toHaveAttribute('href', '/auth/login?return_to=%2F')
  })

  it('shows a sign-in error from the callback', () => {
    renderAt('/login?error=state_mismatch')
    expect(screen.getByRole('alert')).toHaveTextContent(/could not be verified/i)
  })

  it('greets a signed-in user', async () => {
    mockFetch(200, {
      user: { id: '1', email: 'a@example.com', name: 'Alice', avatarUrl: null },
      family: { id: 'f', name: 'Ivanovs', role: 'owner' },
    })
    renderAt('/')
    expect(await screen.findByText('Alice')).toBeInTheDocument()
    expect(screen.getByText(/calendar for ivanovs/i)).toBeInTheDocument()
  })
})
