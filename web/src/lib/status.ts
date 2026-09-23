/**
 * A delivery's status, as every page that shows one names it.
 *
 * Shared because the list, the counters and the detail page all display the
 * same four values, and the labels on the counters are deliberately the same
 * words as the tags in the table below them. Two copies of that mapping is how
 * the two drift, and the drift is invisible until somebody is comparing a
 * number with the rows it is supposed to describe.
 */

import type { Messages } from './messages'

/**
 * The four statuses, in the order the filter offers them.
 *
 * The values are the English enums the store filters on — what ends up in
 * `?status=`. Translating them would change what the API accepts, which is why
 * only the label is translated.
 */
export const STATUSES = ['queued', 'sending', 'sent', 'failed'] as const

/** The translated name of a status. */
export function statusLabel(t: Messages, status: string): string {
  switch (status) {
    case 'queued':
      return t.StatusQueued
    case 'sending':
      return t.StatusSending
    case 'sent':
      return t.StatusSent
    case 'failed':
      return t.StatusFailed
    default:
      // A status this build does not know — a row written by a newer version,
      // or a hand-edited database. Shown as it is rather than blank, because a
      // blank cell in a status column reads as "fine".
      return status
  }
}

/**
 * A status's colour.
 *
 * The same four tones the server-rendered tags use: queued and sending are the
 * two states that are still moving, sent is done, failed is the one to look at.
 */
export function statusTone(status: string): 'warn' | 'info' | 'on' | 'bad' | undefined {
  switch (status) {
    case 'queued':
      return 'warn'
    case 'sending':
      return 'info'
    case 'sent':
      return 'on'
    case 'failed':
      return 'bad'
    default:
      return undefined
  }
}
