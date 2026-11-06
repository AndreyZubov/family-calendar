import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, isApiError, unwrap, type CalendarEvent, type EventInput, type Me } from './client'

export const queryKeys = {
  me: ['me'] as const,
  members: ['members'] as const,
  events: ['events'] as const,
  eventsInRange: (from: Date, to: Date) =>
    ['events', from.toISOString(), to.toISOString()] as const,
  event: (id: string) => ['event', id] as const,
}

// ---------------------------------------------------------------------------
// Session

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

// ---------------------------------------------------------------------------
// Family

export function useMembers() {
  return useQuery({
    queryKey: queryKeys.members,
    queryFn: async () => unwrap(await api.GET('/api/family/members')).items,
  })
}

export function useCreateFamily() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (name: string) => unwrap(await api.POST('/api/family', { body: { name } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.me }),
  })
}

export function useCreateInvite() {
  return useMutation({
    mutationFn: async () => unwrap(await api.POST('/api/family/invites')),
  })
}

export function useAcceptInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (token: string) =>
      unwrap(await api.POST('/api/invites/accept', { body: { token } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.me }),
  })
}

/** Removes a member (owner) or leaves the family (yourself). */
export function useRemoveMember() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (userId: string) => {
      unwrap(await api.DELETE('/api/family/members/{userId}', { params: { path: { userId } } }))
    },
    onSuccess: async () => {
      // Participants on events change too (cascade on the server).
      await Promise.all([
        qc.invalidateQueries({ queryKey: queryKeys.members }),
        qc.invalidateQueries({ queryKey: queryKeys.me }),
        qc.invalidateQueries({ queryKey: queryKeys.events }),
      ])
    },
  })
}

// ---------------------------------------------------------------------------
// Events

/** Events overlapping [from, to). Keeps showing the previous month while the
 * next one loads, so the grid does not flash empty. */
export function useEvents(from: Date, to: Date) {
  return useQuery({
    queryKey: queryKeys.eventsInRange(from, to),
    queryFn: async () =>
      unwrap(
        await api.GET('/api/events', {
          params: { query: { from: from.toISOString(), to: to.toISOString() } },
        }),
      ).items,
    placeholderData: keepPreviousData,
  })
}

export function useEvent(id: string) {
  return useQuery({
    queryKey: queryKeys.event(id),
    queryFn: async () => unwrap(await api.GET('/api/events/{id}', { params: { path: { id } } })),
  })
}

/** Creates an event, or replaces it when `id` is given. */
export function useSaveEvent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async ({
      id,
      input,
    }: {
      id?: string
      input: EventInput
    }): Promise<CalendarEvent> =>
      id
        ? unwrap(await api.PUT('/api/events/{id}', { params: { path: { id } }, body: input }))
        : unwrap(await api.POST('/api/events', { body: input })),
    onSuccess: async (ev) => {
      qc.setQueryData(queryKeys.event(ev.id), ev)
      await qc.invalidateQueries({ queryKey: queryKeys.events })
    },
  })
}

export function useDeleteEvent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (id: string) => {
      unwrap(await api.DELETE('/api/events/{id}', { params: { path: { id } } }))
    },
    onSuccess: async (_, id) => {
      qc.removeQueries({ queryKey: queryKeys.event(id) })
      await qc.invalidateQueries({ queryKey: queryKeys.events })
    },
  })
}
