import { useState, type FormEvent } from 'react'

import { Page, PageHead } from '../components/Page'
import { APIError, session as api } from '../lib/api'
import { useT } from '../lib/i18n'

/**
 * Changing the account's password.
 *
 * ---------------------------------------------------------------------------
 * The success message is the server's and not this page's, and that is the one
 * thing here worth explaining.
 *
 * Changing a password ends every other session — the usual reason to do it is
 * the belief that somebody else knows it, and leaving that person signed in has
 * not done the thing the change was for. How many sessions that was is a fact
 * only the server has, and "did that actually lock them out" is the question
 * the operator is asking. So the sentence is composed there and rendered here,
 * with a local fallback for the case where the response carried none.
 *
 * There is no recovery path on this page and there is deliberately no link to
 * one. Getting back in needs a second credential or a mail route, neither of
 * which this service has, and inventing one badly would be worse than the
 * documented answer, which is to edit the database.
 * ---------------------------------------------------------------------------
 */
export function Password() {
  const t = useT()

  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(event: FormEvent) {
    event.preventDefault()

    if (next !== confirm) {
      setDone('')
      setError(t.Script.PasswordsNoMatch)
      return
    }

    setBusy(true)
    setError('')
    setDone('')

    try {
      const result = await api.changePassword(current, next)
      setDone(result.message || t.Script.PasswordSet)
      setCurrent('')
      setNext('')
      setConfirm('')
    } catch (err) {
      setError(err instanceof APIError && err.message ? err.message : t.ErrPasswordChangeFailed)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Page>
      <PageHead title={t.TitlePassword} description={t.PasswordIntro} />

      <form className="card" onSubmit={onSubmit}>
        <div className="grid">
          <div className="field">
            <label htmlFor="current-password">{t.PasswordCurrent}</label>
            <input
              id="current-password"
              name="current_password"
              type="password"
              autoComplete="current-password"
              required
              autoFocus
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </div>

          <div className="field">
            <label htmlFor="new-password">{t.PasswordNew}</label>
            <input
              id="new-password"
              name="new_password"
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </div>

          {/* Full width, and the rule goes under it: the confirmation is the
              field the rule is about, and a sentence about length sitting above
              the box it constrains is a sentence read too late. */}
          <div className="field full">
            <label htmlFor="confirm-password">{t.PasswordConfirm}</label>
            <input
              id="confirm-password"
              name="confirm_password"
              type="password"
              autoComplete="new-password"
              required
              minLength={8}
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
            <p className="desc">{t.PasswordRule}</p>
          </div>
        </div>

        {/* Both messages sit inside the card, above the footer, so that a
            failure is next to the form it is about rather than at the top of a
            page the operator has scrolled past. */}
        {error && <p className="flash bad inset">{error}</p>}
        {done && <p className="flash ok inset">{done}</p>}

        <div className="form-foot">
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {t.PasswordSubmit}
            </button>
          </div>
        </div>
      </form>
    </Page>
  )
}
