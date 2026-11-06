import { Link, useSearchParams } from 'react-router'

import type { CalendarEvent } from '../api/client'
import { useEvents } from '../api/queries'
import { Avatar } from '../components/Avatar'
import { ErrorBox } from '../components/Status'
import {
  addMonths,
  formatDay,
  formatEventTime,
  formatMonth,
  fromKeys,
  isSameDay,
  monthGrid,
  occursOn,
  startOfMonth,
  toDateKey,
  weekdayNames,
} from '../lib/dates'

const MAX_CHIPS = 3 // events shown per day cell before "+N more"

/**
 * Month view with a list of the selected day's events. The month and day
 * live in the URL (?day=2026-11-10), so the back button and shared links
 * work.
 */
export function CalendarPage() {
  const [params, setParams] = useSearchParams()
  const today = new Date()
  const selected = fromKeys(params.get('day') ?? '') ?? today
  const month = startOfMonth(selected)
  // Cheap to rebuild every render; the query key uses the ISO strings, so
  // new Date objects do not trigger refetches.
  const { days, from, to } = monthGrid(month)

  const events = useEvents(from, to)
  const list = events.data ?? []

  const select = (d: Date) => {
    setParams({ day: toDateKey(d) })
  }
  const dayEvents = list.filter((ev) => occursOn(ev, selected))

  return (
    <div className="calendar">
      <div className="calendar-toolbar">
        <h1>{formatMonth(month)}</h1>
        <div className="toolbar-buttons">
          <button
            type="button"
            className="secondary"
            aria-label="Previous month"
            onClick={() => {
              select(addMonths(month, -1))
            }}
          >
            ‹
          </button>
          <button
            type="button"
            className="secondary"
            onClick={() => {
              select(today)
            }}
          >
            Today
          </button>
          <button
            type="button"
            className="secondary"
            aria-label="Next month"
            onClick={() => {
              select(addMonths(month, 1))
            }}
          >
            ›
          </button>
        </div>
        <Link className="button" to={`/events/new?date=${toDateKey(selected)}`}>
          New event
        </Link>
      </div>

      {events.isError && <ErrorBox error={events.error} onRetry={() => void events.refetch()} />}

      <div
        className="month"
        role="grid"
        aria-label={formatMonth(month)}
        aria-busy={events.isFetching}
      >
        <div className="month-row weekdays" role="row">
          {weekdayNames.map((w) => (
            <div key={w} role="columnheader" className="weekday">
              {w}
            </div>
          ))}
        </div>
        {Array.from({ length: 6 }, (_, week) => (
          <div className="month-row" role="row" key={week}>
            {days.slice(week * 7, week * 7 + 7).map((d) => {
              const evs = list.filter((ev) => occursOn(ev, d))
              const classes = [
                'day',
                d.getMonth() !== month.getMonth() && 'other-month',
                isSameDay(d, today) && 'today',
                isSameDay(d, selected) && 'selected',
              ]
                .filter(Boolean)
                .join(' ')
              return (
                <button
                  type="button"
                  role="gridcell"
                  key={d.getTime()}
                  className={classes}
                  aria-selected={isSameDay(d, selected)}
                  aria-current={isSameDay(d, today) ? 'date' : undefined}
                  aria-label={`${formatDay(d)}, ${String(evs.length)} event${evs.length === 1 ? '' : 's'}`}
                  onClick={() => {
                    select(d)
                  }}
                >
                  <span className="day-number">{d.getDate()}</span>
                  {evs.slice(0, MAX_CHIPS).map((ev) => (
                    <span key={ev.id} className={ev.allDay ? 'chip all-day' : 'chip'}>
                      {ev.title}
                    </span>
                  ))}
                  {evs.length > MAX_CHIPS && (
                    <span className="chip more">+{evs.length - MAX_CHIPS} more</span>
                  )}
                </button>
              )
            })}
          </div>
        ))}
      </div>

      <section className="day-panel" aria-label={`Events on ${formatDay(selected)}`}>
        <h2>{formatDay(selected)}</h2>
        {events.isPending ? (
          <p className="muted">Loading…</p>
        ) : dayEvents.length === 0 ? (
          <p className="muted">
            Nothing planned.{' '}
            <Link to={`/events/new?date=${toDateKey(selected)}`}>Add an event</Link>
          </p>
        ) : (
          <ul className="event-list">
            {dayEvents.map((ev) => (
              <EventRow key={ev.id} ev={ev} />
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function EventRow({ ev }: { ev: CalendarEvent }) {
  return (
    <li>
      <Link to={`/events/${ev.id}`} className="event-row">
        <span className="event-time">{formatEventTime(ev)}</span>
        <span className="event-title">{ev.title}</span>
        <span className="avatars" aria-label={`${String(ev.participants.length)} participants`}>
          {ev.participants.map((p) => (
            <span key={p.userId} title={p.name}>
              <Avatar name={p.name} url={p.avatarUrl} size={24} />
            </span>
          ))}
        </span>
      </Link>
    </li>
  )
}
