import type { Me } from '../api/client'
import { useMe } from '../api/queries'

/**
 * The signed-in user's data. Only call inside RequireUser, where it is
 * guaranteed to be loaded.
 */
export function useCurrentUser(): Me {
  const me = useMe()
  if (!me.data) throw new Error('useCurrentUser used outside RequireUser')
  return me.data
}
