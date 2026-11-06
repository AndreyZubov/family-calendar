import { Navigate } from 'react-router'

import { useLogout, useMe } from '../api/queries'

// Placeholder screens; the real calendar UI comes in step 12.
export function HomePage() {
  const me = useMe()
  const logout = useLogout()

  if (me.isPending) return <p className="center muted">Loading…</p>
  if (me.isError) return <p className="center error">Could not reach the server.</p>
  if (me.data === null) return <Navigate to="/login" replace />

  const { user, family } = me.data
  return (
    <main className="page">
      <header className="topbar">
        <strong>{family?.name ?? 'Family Calendar'}</strong>
        <span className="muted">{user.name}</span>
        <button
          type="button"
          onClick={() => {
            logout.mutate()
          }}
          disabled={logout.isPending}
        >
          Sign out
        </button>
      </header>
      {family ? (
        <p>Calendar for {family.name} — coming next.</p>
      ) : (
        <p>You are not in a family yet. Creating or joining one comes next.</p>
      )}
    </main>
  )
}
