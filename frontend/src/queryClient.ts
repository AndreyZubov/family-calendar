import { QueryClient } from '@tanstack/react-query'

import { isApiError } from './api/client'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Data is considered fresh for 30 s: navigating between pages does
        // not refetch everything, but returning to the tab later does.
        staleTime: 30_000,
        // Retrying a 4xx (not found, validation, unauthenticated) never helps.
        retry: (failureCount, err) =>
          !(isApiError(err) && err.status >= 400 && err.status < 500) && failureCount < 2,
      },
    },
  })
}
