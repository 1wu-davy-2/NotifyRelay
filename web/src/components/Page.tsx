import type { ReactNode } from 'react'

import { APIError } from '../lib/api'
import { useT } from '../lib/i18n'

/**
 * The page furniture: a heading, an empty state, a failure.
 *
 * These three appear on every page and they are here rather than repeated so
 * that they cannot drift — a heading with its description in one place and
 * without it in another is the sort of difference nobody decides and everybody
 * notices.
 */

/**
 * The reading column.
 *
 * Every page renders one. `wide` is for the two that are mostly a table — the
 * delivery list and the audit trail — where the default measure would push a
 * row's last column off the side.
 */
export function Page({
  children,
  wide,
  className,
}: {
  children: ReactNode
  wide?: boolean
  /** For the one page that carries a vocabulary of its own. See .docs in app.css. */
  className?: string
}) {
  const classes = [wide ? 'wide' : '', className ?? ''].filter(Boolean).join(' ')
  return <main className={classes || undefined}>{children}</main>
}

export function PageHead({
  title,
  description,
  actions,
}: {
  title: string
  /** A string, or a <Copy> element for the copy that carries markup. */
  description?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="page-head">
      <div>
        <h1>{title}</h1>
        {description && <p className="page-desc">{description}</p>}
      </div>
      {actions}
    </div>
  )
}

/**
 * Nothing here yet.
 *
 * `action` is what the operator can do about it, and it is optional because
 * some empty states have no action — an audit trail that is empty because
 * nothing has happened is not a problem to be fixed.
 */
export function Empty({
  icon,
  title,
  body,
  action,
}: {
  icon?: ReactNode
  title: string
  /** A string, or a <Copy> element for the copy that carries markup. */
  body: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="empty">
      {icon && <div className="icon">{icon}</div>}
      <h3>{title}</h3>
      <p>{body}</p>
      {action}
    </div>
  )
}

/**
 * A page's data could not be read.
 *
 * The message is the server's own, which is already in the reader's language —
 * the handlers answer with a translated sentence from the same table this
 * interface renders from. Only when there is no server message does this fall
 * back to its own.
 */
export function LoadFailed({ error }: { error: Error }) {
  const t = useT()

  const message = error instanceof APIError && error.message ? error.message : t.ErrChannelsUnreadable

  return (
    <div className="card">
      <div className="empty">
        <h3>{t.TitleError}</h3>
        <p>{message}</p>
      </div>
    </div>
  )
}

/**
 * A status pill.
 *
 * `tone` maps to the same four states the server-rendered pages use, so a
 * failed delivery is the same red in both interfaces.
 */
export function Tag({
  tone,
  mono,
  children,
}: {
  tone?: 'on' | 'off' | 'warn' | 'bad' | 'info'
  mono?: boolean
  children: ReactNode
}) {
  const classes = ['tag']
  if (tone) classes.push(tone)
  if (mono) classes.push('tag-mono')
  return <span className={classes.join(' ')}>{children}</span>
}
