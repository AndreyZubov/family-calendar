// Date helpers in the user's local time zone. The API speaks UTC instants
// (RFC 3339); everything the user sees and types is local.
//
// All-day events follow the iCalendar convention: startsAt is local midnight
// of the first day and endsAt is local midnight *after* the last day
// (exclusive end). A one-day event on Nov 10 is [Nov 10 00:00, Nov 11 00:00).

export const DAY_MS = 24 * 60 * 60 * 1000

export function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate())
}

/** Adds calendar days (DST-safe: works on local dates, not milliseconds). */
export function addDays(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n, d.getHours(), d.getMinutes())
}

export function startOfMonth(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1)
}

export function addMonths(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth() + n, 1)
}

/** Monday of the week containing d. */
export function startOfWeek(d: Date): Date {
  const day = (d.getDay() + 6) % 7 // Monday = 0 … Sunday = 6
  return addDays(startOfDay(d), -day)
}

export function isSameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  )
}

/**
 * The 6×7 days shown for a month (weeks start on Monday), plus the
 * half-open range [from, to) to request from the API.
 */
export function monthGrid(month: Date): { days: Date[]; from: Date; to: Date } {
  const first = startOfWeek(startOfMonth(month))
  const days = Array.from({ length: 42 }, (_, i) => addDays(first, i))
  return { days, from: first, to: addDays(first, 42) }
}

const pad = (n: number) => String(n).padStart(2, '0')

/** "2026-11-10" in local time — the value format of <input type="date">. */
export function toDateKey(d: Date): string {
  return `${String(d.getFullYear())}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** "18:30" in local time — the value format of <input type="time">. */
export function toTimeKey(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** Parses "YYYY-MM-DD" (+ optional "HH:MM") as local time; null if invalid. */
export function fromKeys(date: string, time = '00:00'): Date | null {
  const dm = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date)
  const tm = /^(\d{2}):(\d{2})$/.exec(time)
  if (!dm || !tm) return null
  const [y, mo, d, h, mi] = [dm[1], dm[2], dm[3], tm[1], tm[2]].map(Number) as [
    number,
    number,
    number,
    number,
    number,
  ]
  const result = new Date(y, mo - 1, d, h, mi)
  // Reject overflow such as 2026-02-31 (JS would roll it into March).
  return result.getMonth() === mo - 1 && result.getDate() === d ? result : null
}

interface Span {
  startsAt: string
  endsAt: string
}

/** Whether an event is visible on the given local day. */
export function occursOn(ev: Span, day: Date): boolean {
  const dayStart = startOfDay(day).getTime()
  const dayEnd = addDays(startOfDay(day), 1).getTime()
  const start = Date.parse(ev.startsAt)
  const end = Date.parse(ev.endsAt)
  // Overlap with [dayStart, dayEnd); zero-length events count on their start day.
  return start < dayEnd && (end > dayStart || (start === end && start >= dayStart))
}

/** Converts the form's local values to API instants. */
export function toInstants(v: {
  allDay: boolean
  startDate: string
  startTime: string
  endDate: string
  endTime: string
}): { startsAt: Date; endsAt: Date } | null {
  if (v.allDay) {
    const s = fromKeys(v.startDate)
    const e = fromKeys(v.endDate)
    return s && e ? { startsAt: s, endsAt: addDays(e, 1) } : null // exclusive end
  }
  const s = fromKeys(v.startDate, v.startTime)
  const e = fromKeys(v.endDate, v.endTime)
  return s && e ? { startsAt: s, endsAt: e } : null
}

/** Converts an API event back to the form's local values. */
export function fromInstants(ev: { allDay: boolean; startsAt: string; endsAt: string }) {
  const s = new Date(ev.startsAt)
  const e = new Date(ev.endsAt)
  // For all-day events the stored end is exclusive; the form shows the last day.
  const lastDay = ev.allDay ? addDays(e, -1) : e
  return {
    allDay: ev.allDay,
    startDate: toDateKey(s),
    startTime: toTimeKey(s),
    endDate: toDateKey(ev.allDay && lastDay < s ? s : lastDay),
    endTime: toTimeKey(e),
  }
}

const monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' })
const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long' })
const timeFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' })
const shortDateFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short' })
const weekdayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short' })

export const formatMonth = (d: Date) => monthFmt.format(d)
export const formatDay = (d: Date) => dayFmt.format(d)
export const formatTime = (d: Date) => timeFmt.format(d)
export const formatShortDate = (d: Date) => shortDateFmt.format(d)

/** Short weekday names starting on Monday, in the user's locale. */
export const weekdayNames = Array.from({ length: 7 }, (_, i) =>
  weekdayFmt.format(new Date(2024, 0, 1 + i)),
) // 2024-01-01 was a Monday

/** "18:00 – 20:00", "All day", or with dates when the event spans days. */
export function formatEventTime(ev: { allDay: boolean; startsAt: string; endsAt: string }): string {
  const s = new Date(ev.startsAt)
  const e = new Date(ev.endsAt)
  if (ev.allDay) {
    const last = addDays(e, -1)
    return isSameDay(s, last) || last < s
      ? 'All day'
      : `${formatShortDate(s)} – ${formatShortDate(last)}`
  }
  if (isSameDay(s, e)) return `${formatTime(s)} – ${formatTime(e)}`
  return `${formatShortDate(s)} ${formatTime(s)} – ${formatShortDate(e)} ${formatTime(e)}`
}
