import { errorMessage } from '../api/errors'

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <p className="status muted" role="status">
      {label}
    </p>
  )
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div className="status" role="alert">
      <p className="error">{errorMessage(error)}</p>
      {onRetry && (
        <button type="button" className="secondary" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  )
}
