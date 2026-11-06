import { describe, expect, it } from 'vitest'

import {
  addDays,
  fromInstants,
  fromKeys,
  monthGrid,
  occursOn,
  startOfWeek,
  toDateKey,
  toInstants,
  toTimeKey,
} from './dates'

const local = (y: number, m: number, d: number, h = 0, mi = 0) => new Date(y, m - 1, d, h, mi)
const span = (s: Date, e: Date) => ({ startsAt: s.toISOString(), endsAt: e.toISOString() })

describe('monthGrid', () => {
  it('starts on the Monday before the 1st and covers 6 weeks', () => {
    const { days, from, to } = monthGrid(local(2026, 11, 15))
    expect(days).toHaveLength(42)
    expect(toDateKey(from)).toBe('2026-10-26') // Nov 1, 2026 is a Sunday
    expect(days[0]?.getDay()).toBe(1)
    expect(toDateKey(to)).toBe('2026-12-07')
  })

  it('starts on the 1st when the month begins on a Monday', () => {
    expect(toDateKey(monthGrid(local(2026, 6, 10)).from)).toBe('2026-06-01')
  })
})

describe('startOfWeek / addDays', () => {
  it('treats Sunday as the end of the week', () => {
    expect(toDateKey(startOfWeek(local(2026, 11, 8)))).toBe('2026-11-02')
  })

  it('adds calendar days across month ends', () => {
    expect(toDateKey(addDays(local(2026, 1, 31), 1))).toBe('2026-02-01')
    expect(toDateKey(addDays(local(2026, 3, 1), -1))).toBe('2026-02-28')
  })
})

describe('fromKeys', () => {
  it('parses local date and time', () => {
    const d = fromKeys('2026-11-10', '18:30')
    expect(d && [toDateKey(d), toTimeKey(d)]).toEqual(['2026-11-10', '18:30'])
  })

  it.each(['2026-02-31', '2026-13-01', '10.11.2026', ''])('rejects %j', (s) => {
    expect(fromKeys(s)).toBeNull()
  })
})

describe('occursOn', () => {
  const day = local(2026, 11, 10)

  it.each([
    ['inside the day', span(local(2026, 11, 10, 18), local(2026, 11, 10, 20)), true],
    ['spanning midnight into the day', span(local(2026, 11, 9, 22), local(2026, 11, 10, 1)), true],
    ['ending exactly at midnight before', span(local(2026, 11, 9, 22), local(2026, 11, 10)), false],
    ['starting at next midnight', span(local(2026, 11, 11), local(2026, 11, 11, 1)), false],
    ['zero-length on the day', span(local(2026, 11, 10, 9), local(2026, 11, 10, 9)), true],
    ['multi-day covering the day', span(local(2026, 11, 8), local(2026, 11, 13)), true],
  ])('%s → %s', (_, ev, expected) => {
    expect(occursOn(ev, day)).toBe(expected)
  })
})

describe('toInstants / fromInstants', () => {
  it('stores all-day events with an exclusive end and shows the last day back', () => {
    const r = toInstants({
      allDay: true,
      startDate: '2026-11-10',
      endDate: '2026-11-12',
      startTime: '',
      endTime: '',
    })
    expect(r?.startsAt).toEqual(local(2026, 11, 10))
    expect(r?.endsAt).toEqual(local(2026, 11, 13))

    const back = fromInstants({
      allDay: true,
      startsAt: local(2026, 11, 10).toISOString(),
      endsAt: local(2026, 11, 13).toISOString(),
    })
    expect(back).toMatchObject({ startDate: '2026-11-10', endDate: '2026-11-12' })
  })

  it('round-trips timed events', () => {
    const values = {
      allDay: false,
      startDate: '2026-11-10',
      startTime: '18:00',
      endDate: '2026-11-10',
      endTime: '20:30',
    }
    const r = toInstants(values)
    expect(r).not.toBeNull()
    if (!r) return
    expect(
      fromInstants({
        allDay: false,
        startsAt: r.startsAt.toISOString(),
        endsAt: r.endsAt.toISOString(),
      }),
    ).toEqual(values)
  })

  it('returns null for incomplete input', () => {
    expect(
      toInstants({
        allDay: false,
        startDate: '2026-11-10',
        startTime: '',
        endDate: '',
        endTime: '',
      }),
    ).toBeNull()
  })
})
