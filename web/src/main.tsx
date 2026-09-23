import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'

import './styles/tokens.css'
import './styles/app.css'

import { App } from './App'
import { I18nProvider } from './lib/i18n'
import { SessionProvider } from './lib/session'
import { init as initTheme } from './lib/theme'

// Before anything renders.
//
// index.html ships no `data-theme`, and tokens.css holds the page invisible
// while the attribute is missing — which is how the wrong theme is kept from
// flashing without the inline script the CSP forbids. This is the statement
// that ends that state, so it goes first: a module body runs synchronously
// after its imports are evaluated, and none of those render anything.
initTheme()

// Where the interface is mounted. The Go router serves the shell at every page
// path under /admin; changing this without changing internal/admin/spa.go
// produces a page whose links all 404.
const BASENAME = '/admin'

const root = document.getElementById('root')
if (!root) throw new Error('no #root element')

createRoot(root).render(
  <StrictMode>
    <BrowserRouter basename={BASENAME}>
      {/* The order matters, and it is the order of the two questions the
          interface asks before it can draw anything: what do things say, and is
          anybody signed in.

          The copy table is outermost because the sign-in screen needs it too,
          and because /api/i18n answers without a session — that endpoint is
          public for exactly this reason. The session is inside it, so that the
          screen that asks for a password is already able to word itself. */}
      <I18nProvider>
        <SessionProvider>
          <App />
        </SessionProvider>
      </I18nProvider>
    </BrowserRouter>
  </StrictMode>,
)
