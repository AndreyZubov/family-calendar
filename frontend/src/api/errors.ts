import { isApiError } from './client'

// User-facing texts for the stable error codes of the API
// (see backend/api/openapi.yaml).
const messages: Record<string, string> = {
  unauthenticated: 'Your session has expired. Please sign in again.',
  no_family: 'Create or join a family first.',
  already_in_family: 'You already belong to a family.',
  invite_invalid: 'This invite link is invalid, expired or has already been used.',
  owner_cannot_leave: 'The family owner cannot leave the family.',
  forbidden: 'Only the family owner can do this.',
  not_found: 'It no longer exists. It may have been deleted.',
  validation_failed: 'Please check the highlighted fields.',
  rate_limited: 'Too many requests. Please wait a moment and try again.',
  cross_origin: 'The request was blocked for security reasons. Please reload the page.',
  body_too_large: 'The text is too long.',
}

/** A message to show the user for any error thrown by the API client. */
export function errorMessage(err: unknown): string {
  if (isApiError(err)) return messages[err.code] ?? 'Something went wrong. Please try again.'
  return 'Could not reach the server. Check your connection and try again.'
}

/** Field errors from a `validation_failed` response, keyed by API field name. */
export function fieldErrors(err: unknown): Record<string, string> {
  return isApiError(err, 'validation_failed') ? err.fields : {}
}
