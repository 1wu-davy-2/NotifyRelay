/**
 * The light/dark choice.
 *
 * ---------------------------------------------------------------------------
 * Kept in a cookie rather than in localStorage, and the reason is the first
 * paint.
 *
 * The usual way to stop the wrong theme flashing on load is a small inline
 * <script> that reads the saved value and sets the attribute before anything is
 * painted. That script cannot exist here: the admin's CSP is `script-src 'self'`
 * with no 'unsafe-inline', and an inline script is the one thing it forbids.
 *
 * A cookie solves it from the other end. The preference is sent with the
 * request for the shell, the Go process writes it into the <html> element as it
 * serves the file — see applyPreferences in internal/admin/spa.go — and the
 * browser's very first paint is already the right theme. Nothing is deferred
 * and nothing is hidden while it loads.
 *
 * The language works the same way and for a stronger reason: `lang` on <html>
 * is what tells a screen reader which voice to use, and it has to be right in
 * the served markup rather than corrected by a script afterwards.
 *
 * localStorage would be marginally faster to read and cannot be read by the
 * server, which is the whole point.
 * ---------------------------------------------------------------------------
 */

export type Theme = 'dark' | 'light'

/** Where the preference is kept. Read by internal/admin/spa.go as well. */
const COOKIE = 'nr_theme'

/**
 * The theme to use when nothing is stored.
 *
 * Light. This interface is read in a bright room in the middle of a working
 * day, and a page that starts dark is one an operator turns off before they
 * have read anything on it. It was dark, which was the only appearance the
 * server-rendered pages ever had; the switch is one click either way and the
 * choice is remembered.
 *
 * Deliberately not `prefers-color-scheme`: an operator who has set their
 * desktop to dark has said something about their desktop, and a monitoring
 * interface that changes colour under them because of it is one they cannot
 * take a stable screenshot of. The switch is one click and the choice is
 * remembered, which is the same argument the language switcher makes.
 *
 * Changing this value means changing two more, and they are not optional:
 * index.html carries the same default for `npm run dev`, and themeOf in
 * internal/admin/spa.go carries it for every served page. A build that moves
 * only this one flashes the other theme on every load.
 */
const DEFAULT: Theme = 'light'

/** A year. A preference, not a credential — the same lifetime as the language. */
const MAX_AGE = 365 * 24 * 60 * 60

/** Reads a cookie by name, or null. */
function readCookie(name: string): string | null {
  const match = document.cookie.match(new RegExp('(?:^|; )' + name + '=([^;]*)'))
  return match ? decodeURIComponent(match[1]) : null
}

/**
 * Writes a cookie.
 *
 * No `Secure` flag, and that is deliberate rather than an oversight: this
 * service is commonly deployed behind a TLS-terminating proxy on a plain-HTTP
 * internal address, and a cookie marked Secure is one the browser simply never
 * sends there. That presents as "the theme does not stick" rather than as a
 * security setting. The same reasoning the session cookie's own flag follows.
 *
 * `SameSite=Lax` so the cookie travels on an ordinary navigation and not on a
 * cross-site request, and `path=/` so the admin's own pages all see it.
 */
function writeCookie(name: string, value: string): void {
  document.cookie = `${name}=${encodeURIComponent(value)}; path=/; max-age=${MAX_AGE}; SameSite=Lax`
}

/** The theme currently in effect. */
export function current(): Theme {
  return document.documentElement.dataset.theme === 'light' ? 'light' : 'dark'
}

/**
 * Writes a theme into the document, without remembering it.
 *
 * Separate from apply() because the two callers want different halves. The
 * switch wants both. Startup wants only this one — see init().
 *
 * Two things follow the theme, not one. The attribute drives every colour on
 * the page; the meta tag drives the browser's own chrome — the address bar on a
 * phone, the scrollbar on a desktop. A light interface under a dark bar is the
 * detail that makes a page look unfinished, and it is invisible in a screenshot
 * of the page itself.
 */
function reflect(theme: Theme): void {
  document.documentElement.dataset.theme = theme

  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', theme === 'light' ? '#f6f7f9' : '#0a0c12')
}

/**
 * Applies a theme and remembers it. This is what the switch calls.
 */
export function apply(theme: Theme): void {
  reflect(theme)

  try {
    writeCookie(COOKIE, theme)
  } catch {
    // A browser configured to block site data throws on the write. Not being
    // able to remember the choice is not a reason to refuse to make it: the
    // page is themed for this visit and the next one falls back to the default.
  }
}

/**
 * Reconciles the document with the stored theme. Called before the first
 * render.
 *
 * It always reflects, even when the attribute already matches, and that is the
 * whole reason this is not `if (attribute !== theme) apply(theme)`.
 *
 * On a normal page load the server has already written `data-theme` into the
 * markup — that is what stops the flash — but it does not write the meta tag,
 * which index.html carries with the default. So a stored theme that is not the
 * default leaves the attribute matching and the meta not, and a version of this
 * that skipped on a matching attribute left every reload of the other theme
 * with a browser chrome from this one. The bug is invisible on the page and
 * obvious on a phone.
 *
 * Reflecting without persisting is also what keeps this from writing the cookie
 * again on every single page load.
 */
export function init(): void {
  const stored = readCookie(COOKIE)
  reflect(stored === 'dark' || stored === 'light' ? stored : DEFAULT)
}

/** The theme a toggle should switch to. */
export function other(): Theme {
  return current() === 'dark' ? 'light' : 'dark'
}
