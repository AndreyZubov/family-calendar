import { Navigate, Outlet, useLocation } from 'react-router'

import { useMe } from '../api/queries'
import { FamilySetup } from '../pages/FamilySetup'
import { ErrorBox, Loading } from './Status'
import { useCurrentUser } from './useCurrentUser'

/** Renders child routes only for a signed-in user; others go to /login. */
export function RequireUser() {
  const me = useMe()
  const location = useLocation()

  if (me.isPending) return <Loading />
  if (me.isError) return <ErrorBox error={me.error} onRetry={() => void me.refetch()} />
  if (me.data === null) {
    const returnTo = location.pathname + location.search
    return <Navigate to={`/login?return_to=${encodeURIComponent(returnTo)}`} replace />
  }
  return <Outlet />
}

/** Renders child routes only for a user with a family; others see setup. */
export function RequireFamily() {
  const { family } = useCurrentUser()
  return family ? <Outlet /> : <FamilySetup />
}
