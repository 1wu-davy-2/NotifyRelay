import { Outlet } from 'react-router-dom'

import { LanguageSwitch } from './LanguageSwitch'
import { Sidebar } from './Sidebar'
import { ThemeToggle } from './ThemeToggle'
import { UserMenu } from './UserMenu'

/**
 * The frame every page sits in.
 *
 * A route element with an <Outlet> rather than a component each page wraps
 * itself in. The server-rendered templates take the same approach with their
 * "nav" and "foot" blocks, for the same reason: each page then reads as its own
 * content and nothing else, and adding a page cannot produce a frame that is
 * subtly different from the others.
 *
 * The top bar carries the language switch, the theme switch and the account —
 * the arrangement the design calls for, and a change from the server-rendered
 * shell, which keeps the language switch in the sidebar footer. Both are
 * defensible; matching the design is the point of this interface.
 */
export function Shell() {
  return (
    <div className="shell">
      <Sidebar />

      <header className="topbar">
        <LanguageSwitch />
        <ThemeToggle />
        <UserMenu />
      </header>

      <div className="content">
        <Outlet />
      </div>
    </div>
  )
}
