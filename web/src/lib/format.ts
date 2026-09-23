/**
 * Rendering values the way the Go templates render them.
 *
 * These mirror methods on i18n.Messages — Since and Stamp — rather than
 * reimplementing them from scratch, and the mirroring is the point: the two
 * interfaces show the same deliveries and the same timestamps, and an operator
 * comparing them side by side should not be able to tell which one they are
 * looking at.
 *
 * They take the message table as an argument because the sentences live in it.
 * "3 minutes ago" and "3 分钟前" are not the same sentence with the words
 * swapped — Chinese has no plural and counts in a different order — so the
 * format string is copy, and copy belongs in the table.
 */

import type { Messages } from './messages'

/**
 * How long ago a time was.
 *
 * Blank for an unset field rather than a count of years since year zero: a
 * delivery that has never been sent has no sent time, and "1970-01-01" in that
 * cell reads as a bug in the interface.
 *
 * The thresholds match i18n.Messages.Since exactly. They are duplicated here
 * because the alternative is a round trip for a string the browser already has
 * everything it needs to build.
 */
export function since(m: Messages, iso: string | undefined | null): string {
  if (!iso) return ''

  const then = new Date(iso)
  if (Number.isNaN(then.getTime())) return ''

  const seconds = (Date.now() - then.getTime()) / 1000
  // A clock skewed slightly ahead of the server's produces a negative age, and
  // "-1 minutes ago" is worse than saying it just happened.
  if (seconds < 60) return m.TimeJustNow
  if (seconds < 3600) return fill(m.TimeMinutesAgo, Math.floor(seconds / 60))
  if (seconds < 48 * 3600) return fill(m.TimeHoursAgo, Math.floor(seconds / 3600))
  return fill(m.TimeDaysAgo, Math.floor(seconds / 86400))
}

/**
 * A timestamp, in the reader's own timezone.
 *
 * Deliberately not `toLocaleString`: that follows the browser's locale, which
 * is a third language setting independent of the two the interface has, and it
 * produces a different format on every machine. The Go side formats
 * `2006-01-02 15:04:05` local, and so does this.
 */
export function stamp(iso: string | undefined | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''

  const p = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ` +
    `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  )
}

/**
 * Substitutes into a copy string.
 *
 * The Go tables carry `%s` and `%d`, and so do these — one placeholder syntax
 * for one table. A string with no placeholder and no arguments comes back
 * unchanged, which is what makes this safe to call on every string without
 * checking first.
 */
export function fill(template: string, ...values: Array<string | number>): string {
  let i = 0
  return template.replace(/%[sd]/g, () => (i < values.length ? String(values[i++]) : ''))
}
