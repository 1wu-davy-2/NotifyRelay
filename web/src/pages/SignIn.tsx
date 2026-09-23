import { useState, type FormEvent } from 'react'
import { Navigate, useLocation } from 'react-router-dom'

import { AuthCard } from '../components/AuthCard'
import { APIError, session as api } from '../lib/api'
import { useT } from '../lib/i18n'
import { useAuth } from '../lib/session'

/**
 * Signing in.
 *
 * ---------------------------------------------------------------------------
 * Three redirects, and each is a different question.
 *
 *   loading        render nothing. The answer is in flight, and a form that
 *                  appears and is replaced by the page behind it is a flash of
 *                  a password box at somebody who is already signed in.
 *
 *   setupRequired  the first-run form. A deployment with no administrator has
 *                  nothing to sign in to, and the server closes /api/setup the
 *                  moment one exists, so this is a fact about the deployment
 *                  rather than a guess about the visitor.
 *
 *   signed-in      wherever they were going. This is also what runs after a
 *                  successful sign-in — the provider changes state and this
 *                  re-renders — so there is no navigate call on the submit
 *                  path, and no way for the two to disagree about where to go.
 *
 * `from` comes from the router guard, which puts it in the location state when
 * it turns a signed-out visitor away. Without it a session that expires on a
 * delivery detail page lands the operator on the channel list after signing
 * back in, which is a page they did not ask for at a moment they were busy.
 * ---------------------------------------------------------------------------
 */
export function SignIn() {
  const t = useT()
  const auth = useAuth()
  const location = useLocation()

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const from = (location.state as { from?: { pathname: string } } | null)?.from?.pathname ?? '/'

  if (auth.state.status === 'loading') return null
  if (auth.state.status === 'error') return <AuthCard>{auth.state.error.message}</AuthCard>
  if (auth.state.status === 'anonymous' && auth.state.setupRequired) {
    return <Navigate to="/setup" replace />
  }
  if (auth.state.status === 'signed-in') return <Navigate to={from} replace />

  async function onSubmit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')

    try {
      auth.adopt(await api.login(username, password))
      // No navigation here: the redirect above fires when the state changes.
    } catch (err) {
      setError(err instanceof APIError && err.message ? err.message : t.ErrBadCredentials)
      // The password is cleared and the username is not. Retrying is the common
      // case and the username is almost always right; a mistyped one is visible
      // in the box, and a mistyped password is not.
      setPassword('')
      setBusy(false)
    }
  }

  return (
    <AuthCard>
      <p className="muted">{t.LoginSubtitle}</p>

      <form onSubmit={onSubmit}>
        <label htmlFor="username">{t.LoginUsername}</label>
        <input
          id="username"
          name="username"
          autoComplete="username"
          autoFocus
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />

        <label htmlFor="password">{t.LoginPassword}</label>
        <input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />

        <button type="submit" className="btn btn-primary" disabled={busy}>
          {t.LoginSubmit}
        </button>
      </form>

      {/* Below the button rather than above it: the form is two fields, and a
          banner that pushes them down on the second attempt moves the box the
          operator is about to type into. */}
      {error && <p className="flash bad inset">{error}</p>}
    </AuthCard>
  )
}
