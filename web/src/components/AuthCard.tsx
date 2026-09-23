import type { ReactNode } from 'react'

import { useT } from '../lib/i18n'
import { LanguageSwitch } from './LanguageSwitch'

/**
 * The frame the two signed-out screens sit in.
 *
 * ---------------------------------------------------------------------------
 * These are the only pages that do not render inside the shell, and the reason
 * is the shell itself: it carries the sidebar, the account menu and the
 * checklist link, and every one of those is a promise that there is a session
 * behind it. A sign-in form with a navigation bar beside it is a page offering
 * to take you somewhere you cannot go.
 *
 * So the frame is a centred card with the product's name on it, which is the
 * arrangement the server-rendered sign-in page had — kept rather than
 * reinvented, because it is the one thing on this screen an operator has
 * already seen.
 *
 * The language switcher is inside the card and not in a corner, which is where
 * it sits everywhere else. The person who needs it is the person who cannot
 * read the form, and the form is the card.
 * ---------------------------------------------------------------------------
 */
export function AuthCard({ children }: { children: ReactNode }) {
  const t = useT()

  return (
    <main className="centred">
      <div className="card login">
        <h1>{t.AppName}</h1>
        {children}
        <LanguageSwitch />
      </div>
    </main>
  )
}
