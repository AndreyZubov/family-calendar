import { useState } from 'react'
import { Link, useNavigate } from 'react-router'

import { errorMessage } from '../api/errors'
import { useAcceptInvite, useMe } from '../api/queries'
import { ErrorBox, Loading } from '../components/Status'

const STORAGE_KEY = 'pendingInvite'

/**
 * Takes the invite token from the URL fragment (/invite#<token>) and keeps
 * it in sessionStorage, then removes it from the address bar. The fragment is
 * never sent to the server, and sessionStorage survives the sign-in redirect
 * through Google, so the token reaches the API only when the user clicks
 * "Join".
 */
function takeToken(): string | null {
  const fromHash = window.location.hash.slice(1)
  if (fromHash) {
    try {
      sessionStorage.setItem(STORAGE_KEY, fromHash)
    } catch {
      // Storage disabled: still usable for this page view.
    }
    window.history.replaceState(null, '', window.location.pathname)
    return fromHash
  }
  try {
    return sessionStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

function forgetToken() {
  try {
    sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
}

export function InvitePage() {
  const [token] = useState(takeToken)
  const me = useMe()
  const accept = useAcceptInvite()
  const navigate = useNavigate()

  if (!token) {
    return (
      <main className="center">
        <h1>Invite link incomplete</h1>
        <p className="muted">Ask for a new link and open it exactly as you received it.</p>
        <Link to="/">Go to the calendar</Link>
      </main>
    )
  }
  if (me.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} onRetry={() => void me.refetch()} />

  if (me.data === null) {
    return (
      <main className="center">
        <h1>You&apos;re invited</h1>
        <p className="muted">Sign in to join the family calendar.</p>
        <a className="button" href="/auth/login?return_to=%2Finvite">
          Sign in with Google
        </a>
      </main>
    )
  }

  if (me.data.family) {
    forgetToken()
    return (
      <main className="center">
        <h1>You already have a family</h1>
        <p className="muted">
          You are a member of <strong>{me.data.family.name}</strong>. A person can belong to one
          family.
        </p>
        <Link to="/">Go to the calendar</Link>
      </main>
    )
  }

  return (
    <main className="center">
      <h1>Join a family</h1>
      <p className="muted">You will share one calendar with them.</p>
      {accept.isError && <p className="error">{errorMessage(accept.error)}</p>}
      <button
        type="button"
        disabled={accept.isPending}
        onClick={() => {
          accept.mutate(token, {
            onSuccess: () => {
              forgetToken()
              void navigate('/', { replace: true })
            },
          })
        }}
      >
        {accept.isPending ? 'Joining…' : 'Join family'}
      </button>
    </main>
  )
}
