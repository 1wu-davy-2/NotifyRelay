import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { Dialog } from './Dialog'
import { channels } from '../lib/api'
import { useT } from '../lib/i18n'

/**
 * Sends one real notification to one channel, and goes to watch it.
 *
 * ---------------------------------------------------------------------------
 * The one thing on the channel list that proves a notification arrives.
 *
 * "Test connectivity" answers whether a channel can be reached, and for five of
 * the six channel types it cannot even answer that — they have no
 * side-effect-free probe. It never answered the question an operator actually
 * has, which is whether a message lands.
 *
 * So this goes through the ordinary delivery path — queue, worker, channel,
 * attempt history — and comes back with the id of the delivery it created. The
 * operator is sent to that delivery's page, because that page is where "did it
 * work" is answered. A success message here would be the interface claiming a
 * delivery it has not seen yet.
 *
 * What it deliberately cannot do is take a target, a priority, a schedule or a
 * recipient. Everything that makes the notification API worth authenticating is
 * absent: the one thing it can do is the thing an operator looking at this list
 * already has the authority to do — watch what this channel does.
 * ---------------------------------------------------------------------------
 */
export function TestNotifyDialog({
  channel,
  onClose,
}: {
  /** The channel to send to, or null when the dialog is closed. */
  channel: string | null
  onClose: () => void
}) {
  const t = useT()
  const navigate = useNavigate()
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // The placeholders are the server's own suggested wording, and they are also
  // what gets sent if the operator does not type anything — a test notification
  // that says nothing is one nobody can find afterwards.
  const effectiveTitle = title.trim() || t.TestNotifyTitleHint
  const effectiveBody = body.trim() || t.TestNotifyBodyHint

  async function submit() {
    if (!channel) return
    setBusy(true)
    setError(null)
    try {
      const res = await channels.testNotification(channel, effectiveTitle, effectiveBody)
      onClose()
      navigate(res.delivery_id ? `/deliveries/${encodeURIComponent(res.delivery_id)}` : '/deliveries')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={channel !== null}
      onClose={onClose}
      // The heading names the channel, so there is no doubt which one is about
      // to receive a real message.
      title={
        <>
          {t.TestNotifyHeading} — <code>{channel}</code>
        </>
      }
    >
      <p className="muted small">{t.TestNotifyIntro}</p>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <div className="field">
          <label htmlFor="test-notify-title">{t.TestNotifyTitle}</label>
          <input
            id="test-notify-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t.TestNotifyTitleHint}
            autoFocus
          />
        </div>

        <div className="field">
          <label htmlFor="test-notify-body">{t.TestNotifyBody}</label>
          <textarea
            id="test-notify-body"
            rows={3}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t.TestNotifyBodyHint}
          />
        </div>

        {error && <p className="flash bad">{error}</p>}

        <div className="row dialog-actions">
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {t.TestNotifySubmit}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            {t.TestNotifyCancel}
          </button>
        </div>
      </form>
    </Dialog>
  )
}
