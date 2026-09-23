import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'

import { useT } from '../lib/i18n'
import { useSession } from '../lib/session'
import { IconCaret, IconLock, IconSignOut } from './icons'

/**
 * The account menu.
 *
 * Closes on a click outside and on Escape. The click-outside test is on the
 * container rather than on the document with a stopPropagation inside, so a
 * click on the menu's own padding does not close it — which is the behaviour
 * that makes a menu feel like it is not fighting the pointer.
 *
 * Both entries are routes now. The password one used to be an <a> to a
 * server-rendered page, and the comment here said it would move when the switch
 * happened; this is the switch. Signing out is still a button rather than a
 * link, because it is an action that ends in a navigation rather than a
 * destination — a middle-click on "sign out" should not open a tab that is
 * already signed out.
 */
export function UserMenu() {
  const t = useT()
  const session = useSession()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return

    function onPointerDown(event: PointerEvent) {
      if (!ref.current?.contains(event.target as Node)) setOpen(false)
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') setOpen(false)
    }

    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  // The first character, uppercased. Not an initial derived from a full name:
  // the actor is whatever the operator typed at first run, and a single
  // character is the only thing that fits in a 26px circle.
  const initial = session.actor.slice(0, 1).toUpperCase() || '?'

  return (
    <div className="user" ref={ref}>
      <button
        type="button"
        className="user-btn"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-haspopup="menu"
      >
        <span className="avatar">{initial}</span>
        {session.actor}
        <IconCaret className="caret" />
      </button>

      {open && (
        <div className="user-menu" role="menu">
          <Link to="/password" role="menuitem">
            <IconLock />
            {t.NavPassword}
          </Link>
          <div className="sep" />
          <button type="button" role="menuitem" onClick={() => void session.signOut()}>
            <IconSignOut />
            {t.NavSignOut}
          </button>
        </div>
      )}
    </div>
  )
}
