import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, isApiError, unwrap, type Me } from './client'

export const queryKeys = {
  me: ['me'] as const,
}

/**
 * The signed-in user and their family. Resolves to `null` when nobody is
 * signed in (401), so callers can redirect to the login page.
 */
export function useMe() {
  return useQuery({
    queryKey: queryKeys.me,
    queryFn: async (): Promise<Me | null> => {
      try {
        return unwrap(await api.GET('/api/me'))
      } catch (err) {
        if (isApiError(err, 'unauthenticated')) return null
        throw err
      }
    },
  })
}

/** Signs out on the server and forgets all cached data. */
export function useLogout() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      unwrap(await api.POST('/auth/logout'))
    },
    onSuccess: () => {
      qc.setQueryData(queryKeys.me, null)
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== queryKeys.me[0] })
    },
  })
}
