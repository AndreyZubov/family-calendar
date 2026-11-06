import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'

import type { CalendarEvent, FamilyMember } from '../api/client'
import { errorMessage, fieldErrors } from '../api/errors'
import { useDeleteEvent, useEvent, useMembers, useSaveEvent } from '../api/queries'
import { Avatar } from '../components/Avatar'
import { useCurrentUser } from '../components/useCurrentUser'
import { ErrorBox, Loading } from '../components/Status'
import { fromInstants, fromKeys, toDateKey, toInstants } from '../lib/dates'

interface FormValues {
  title: string
  description: string
  allDay: boolean
  startDate: string
  startTime: string
  endDate: string
  endTime: string
  participantIds: string[]
}

/** /events/new?date=YYYY-MM-DD */
export function NewEventPage() {
  const [params] = useSearchParams()
  const { user } = useCurrentUser()
  const date = fromKeys(params.get('date') ?? '')
    ? (params.get('date') ?? '')
    : toDateKey(new Date())
  return (
    <EventForm
      initial={{
        title: '',
        description: '',
        allDay: false,
        startDate: date,
        startTime: '09:00',
        endDate: date,
        endTime: '10:00',
        participantIds: [user.id], // the author usually takes part
      }}
    />
  )
}

/** /events/:id — view and edit. */
export function EditEventPage() {
  const { id = '' } = useParams()
  const ev = useEvent(id)
  if (ev.isPending) return <Loading />
  if (ev.isError) return <ErrorBox error={ev.error} />
  // key: a fresh form (fresh initial state) per event.
  return <EventForm key={ev.data.id} event={ev.data} initial={valuesFrom(ev.data)} />
}

function valuesFrom(ev: CalendarEvent): FormValues {
  return {
    title: ev.title,
    description: ev.description,
    ...fromInstants(ev),
    participantIds: ev.participants.map((p) => p.userId),
  }
}

function EventForm({ event, initial }: { event?: CalendarEvent; initial: FormValues }) {
  const [v, setV] = useState(initial)
  const [clientError, setClientError] = useState<string | null>(null)
  const members = useMembers()
  const save = useSaveEvent()
  const del = useDeleteEvent()
  const navigate = useNavigate()
  const serverFields = fieldErrors(save.error)

  const set = <K extends keyof FormValues>(key: K, value: FormValues[K]) => {
    setV((prev) => {
      const next = { ...prev, [key]: value }
      // Keep the end from falling before the start when the start moves.
      if (key === 'startDate' && next.endDate < next.startDate) next.endDate = next.startDate
      return next
    })
  }

  const backToDay = () => void navigate(`/?day=${v.startDate}`)

  function submit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setClientError(null)
    const times = toInstants(v)
    const problem = !v.title.trim()
      ? 'Give the event a title.'
      : !times
        ? 'Enter a valid start and end.'
        : times.endsAt < times.startsAt
          ? 'The end must not be before the start.'
          : null
    if (problem || !times) {
      setClientError(problem)
      return
    }

    const input = {
      title: v.title,
      description: v.description,
      allDay: v.allDay,
      startsAt: times.startsAt.toISOString(),
      endsAt: times.endsAt.toISOString(),
      participantIds: v.participantIds,
    }
    save.mutate(event ? { id: event.id, input } : { input }, { onSuccess: backToDay })
  }

  function remove() {
    if (!event || !window.confirm(`Delete “${event.title}”? This cannot be undone.`)) return
    del.mutate(event.id, { onSuccess: backToDay })
  }

  const error =
    clientError ??
    (save.isError && Object.keys(serverFields).length === 0 ? errorMessage(save.error) : null)

  return (
    <section className="card narrow">
      <h1>{event ? 'Event' : 'New event'}</h1>
      <form onSubmit={submit} noValidate>
        <label>
          Title
          <input
            value={v.title}
            onChange={(e) => {
              set('title', e.target.value)
            }}
            maxLength={200}
            required
            autoFocus={!event}
            aria-invalid={serverFields.title ? true : undefined}
          />
        </label>
        <FieldError msg={serverFields.title} />

        <label className="inline">
          <input
            type="checkbox"
            checked={v.allDay}
            onChange={(e) => {
              set('allDay', e.target.checked)
            }}
          />
          All day
        </label>

        <fieldset className="when">
          <legend>Starts</legend>
          <input
            type="date"
            aria-label="Start date"
            value={v.startDate}
            onChange={(e) => {
              set('startDate', e.target.value)
            }}
            required
          />
          {!v.allDay && (
            <input
              type="time"
              aria-label="Start time"
              value={v.startTime}
              onChange={(e) => {
                set('startTime', e.target.value)
              }}
              required
            />
          )}
        </fieldset>
        <fieldset className="when">
          <legend>Ends</legend>
          <input
            type="date"
            aria-label="End date"
            value={v.endDate}
            min={v.startDate}
            onChange={(e) => {
              set('endDate', e.target.value)
            }}
            required
          />
          {!v.allDay && (
            <input
              type="time"
              aria-label="End time"
              value={v.endTime}
              onChange={(e) => {
                set('endTime', e.target.value)
              }}
              required
            />
          )}
        </fieldset>
        <FieldError msg={serverFields.startsAt ?? serverFields.endsAt} />

        <label>
          Notes
          <textarea
            value={v.description}
            onChange={(e) => {
              set('description', e.target.value)
            }}
            rows={3}
            maxLength={5000}
          />
        </label>
        <FieldError msg={serverFields.description} />

        <fieldset>
          <legend>Who is coming</legend>
          {members.isPending && <p className="muted">Loading family…</p>}
          {members.isError && <p className="error">{errorMessage(members.error)}</p>}
          {members.data && (
            <ParticipantPicker
              members={members.data}
              selected={v.participantIds}
              onChange={(ids) => {
                set('participantIds', ids)
              }}
            />
          )}
        </fieldset>
        <FieldError msg={serverFields.participantIds} />

        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}

        <div className="actions">
          <button type="submit" disabled={save.isPending}>
            {save.isPending ? 'Saving…' : event ? 'Save changes' : 'Create event'}
          </button>
          <Link to={`/?day=${initial.startDate}`} className="button secondary">
            Cancel
          </Link>
          {event && (
            <button type="button" className="danger" onClick={remove} disabled={del.isPending}>
              {del.isPending ? 'Deleting…' : 'Delete'}
            </button>
          )}
        </div>
        {del.isError && <p className="error">{errorMessage(del.error)}</p>}
      </form>
    </section>
  )
}

function ParticipantPicker({
  members,
  selected,
  onChange,
}: {
  members: FamilyMember[]
  selected: string[]
  onChange: (ids: string[]) => void
}) {
  return (
    <ul className="picker">
      {members.map((m) => {
        const checked = selected.includes(m.userId)
        return (
          <li key={m.userId}>
            <label className="inline">
              <input
                type="checkbox"
                checked={checked}
                onChange={() => {
                  onChange(
                    checked ? selected.filter((id) => id !== m.userId) : [...selected, m.userId],
                  )
                }}
              />
              <Avatar name={m.name} url={m.avatarUrl} size={24} />
              {m.name}
            </label>
          </li>
        )
      })}
    </ul>
  )
}

function FieldError({ msg }: { msg: string | undefined }) {
  return msg ? <p className="field-error">{msg}</p> : null
}
