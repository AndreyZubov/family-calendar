import { Navigate, useSearchParams } from 'react-router'

import { useMe } from '../api/queries'

// Codes from /auth/callback (see backend/api/openapi.yaml).
const loginErrors: Record<string, string> = {
  flow_expired: 'The sign-in took too long or was opened in another browser. Please try again.',
  provider_error: 'Sign-in was cancelled.',
  state_mismatch: 'The sign-in could not be verified. Please try again.',
  email_not_verified: 'Your Google account email is not verified.',
}

export function LoginPage() {
  const [params] = useSearchParams()
  const error = params.get('error')
  // Only local paths; the server enforces the same rule after sign-in.
  const raw = params.get('return_to') ?? '/'
  const returnTo = raw.startsWith('/') && !raw.startsWith('//') ? raw : '/'
  const me = useMe()

  // Already signed in (e.g. a bookmarked /login): go straight in.
  if (me.data) return <Navigate to={returnTo} replace />

  return (
    <main className="center">
      <h1>Family Calendar</h1>
      <p className="muted">Plan your family&apos;s days together.</p>
      {error && (
        <p role="alert" className="error">
          {loginErrors[error] ?? 'Sign-in failed. Please try again.'}
        </p>
      )}
      {/* A plain link, not fetch: sign-in is a full-page redirect flow. */}
      <a className="button" href={`/auth/login?return_to=${encodeURIComponent(returnTo)}`}>
        Sign in with Google
      </a>
    </main>
  )
}
