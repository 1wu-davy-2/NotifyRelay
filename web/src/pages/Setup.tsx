import { useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'

import { AuthCard } from '../components/AuthCard'
import { APIError, setup as api } from '../lib/api'
import { useT } from '../lib/i18n'
import { useAuth } from '../lib/session'

/**
 * The first run.
 *
 * ---------------------------------------------------------------------------
 * The form that creates the account every other request is checked against, and
 * the only one that is offered to whoever reaches the port first. The window it
 * opens is described at the top of internal/admin/setup.go; what matters here
 * is that this screen must not widen it:
 *
 *   it is unreachable once an administrator exists. The server answers 409 and
 *   the interface redirects to the sign-in form — but the redirect is the
 *   courtesy and the 409 is the control.
 *
 *   a submission that loses the race is reported as what it is. Two operators
 *   claiming the same deployment at once is the one failure where the message
 *   has to be exact: the loser is told somebody else got there, not that their
 *   password was unacceptable.
 *
 * ---------------------------------------------------------------------------
 * The confirm field is checked here and not on the server, which is the only
 * validation in this interface that lives on one side. That is deliberate: the
 * server cannot be sent the confirmation without it becoming a second password
 * field, and a rule that only exists to catch a typo is a rule the typist can
 * be told about before the round trip.
 *
 * The floor on the two password boxes is the server's own number, which arrives
 * with the answer that sent the operator here. It used to be a literal 8 in
 * this file, matching a constant in Go and a %d in a translated sentence — three
 * copies of one rule, and the failure mode was a browser accepting a password
 * the server then refused, which reads as a bug in whichever of the two the
 * operator believed.
 * ---------------------------------------------------------------------------
 */
export function Setup() {
  const t = useT()
  const auth = useAuth()

  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  if (auth.state.status === 'loading') return null
  if (auth.state.status === 'error') return <AuthCard>{auth.state.error.message}</AuthCard>
  if (auth.state.status === 'anonymous' && !auth.state.setupRequired) {
    return <Navigate to="/login" replace />
  }

  // Zero on the path where the status could not be fetched, which is the path
  // that shows the sign-in form instead — so this is never read. Kept as a
  // value rather than a null check so the two inputs below stay identical.
  const floor = auth.state.status === 'anonymous' ? auth.state.minPasswordLength : 0
  // Reached after a successful submission, and by anybody who navigates here
  // with a session. Both belong at the checklist rather than the channel list:
  // the person who just created the first account is the person who does not
  // yet know what this service is.
  if (auth.state.status === 'signed-in') return <Navigate to="/start" replace />

  async function onSubmit(event: FormEvent) {
    event.preventDefault()

    if (password !== confirm) {
      setError(t.Script.PasswordsNoMatch)
      return
    }

    setBusy(true)
    setError('')

    try {
      auth.adopt(await api.create(username.trim(), password))
    } catch (err) {
      setError(err instanceof APIError && err.message ? err.message : t.Script.SetupFailed)
      setBusy(false)
    }
  }

  return (
    <AuthCard>
      <p className="muted">{t.SetupIntro}</p>

      <form onSubmit={onSubmit}>
        <label htmlFor="username">{t.SetupUsername}</label>
        <input
          id="username"
          name="username"
          autoComplete="username"
          autoFocus
          required
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />

        <label htmlFor="password">{t.SetupPassword}</label>
        <input
          id="password"
          name="password"
          type="password"
          autoComplete="new-password"
          required
          minLength={floor}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />

        <label htmlFor="confirm">{t.SetupConfirm}</label>
        <input
          id="confirm"
          name="confirm"
          type="password"
          autoComplete="new-password"
          required
          minLength={floor}
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
        />

        <button type="submit" className="btn btn-primary" disabled={busy}>
          {t.SetupSubmit}
        </button>
      </form>

      <p className="desc">{t.SetupPasswordRule}</p>

      {error && <p className="flash bad inset">{error}</p>}
    </AuthCard>
  )
}
