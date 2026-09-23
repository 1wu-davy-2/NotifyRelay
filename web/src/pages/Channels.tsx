import { useCallback, useState } from 'react'
import { Link } from 'react-router-dom'

import { Empty, LoadFailed, Page, PageHead, Tag } from '../components/Page'
import { TestNotifyDialog } from '../components/TestNotifyDialog'
import { IconChannels } from '../components/icons'
import { channels } from '../lib/api'
import { fill } from '../lib/format'
import { useT } from '../lib/i18n'
import { useOnboarding } from '../lib/onboarding'
import { useAsync } from '../lib/useAsync'

/** A message about the last action, shown above the table. */
interface Flash {
  text: string
  ok: boolean
}

/**
 * The channel list.
 *
 * The row's first button is the important one. "Send a test notification" is
 * the only control here that proves a message arrives — the connectivity check
 * beside it answers a narrower question and, for five of the six channel types,
 * does not touch the network at all. See TestNotifyDialog.
 *
 * The state column says three things, not two. "Enabled but not loaded" is a
 * channel that exists and is not in the router, which is what a failed reload
 * looks like from the outside — and an operator looking at a channel that is
 * silently not delivering needs to see that difference.
 */
export function Channels() {
  const t = useT()
  const { refresh: refreshSteps } = useOnboarding()
  const [flash, setFlash] = useState<Flash | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [notifyChannel, setNotifyChannel] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => channels.list(signal), [])
  const { data, error, loading, reload } = useAsync(load)

  /** Runs a row action, reporting the outcome in one place. */
  async function act(name: string, run: () => Promise<string>, ok = true) {
    setBusy(name)
    setFlash(null)
    try {
      const text = await run()
      setFlash({ text, ok })
      reload()
      refreshSteps()
    } catch (err) {
      setFlash({ text: err instanceof Error ? err.message : String(err), ok: false })
    } finally {
      setBusy(null)
    }
  }

  function onTest(name: string) {
    return act(
      name,
      async () => {
        const res = await channels.test(name)
        // The probe answers 200 whether or not it worked, so success is the
        // class and not the status — the same rule the server-rendered page
        // reads out of `ok`.
        return res.ok
          ? fill(t.Script.TestOK, res.detail ?? res.class)
          : fill(t.Script.TestFailed, res.class, res.error ?? res.detail ?? '')
      },
      true,
    )
  }

  function onResetBreaker(name: string) {
    if (!window.confirm(fill(t.Script.ConfirmResetBreaker, name))) return
    return act(name, async () => {
      await channels.resetBreaker(name)
      return fill(t.Script.BreakerResetFlash, name, '')
    })
  }

  function onDelete(name: string) {
    if (!window.confirm(fill(t.Script.ConfirmDeleteChan, name))) return
    return act(name, async () => {
      await channels.remove(name)
      return fill(t.Script.DeletedFlash, name)
    })
  }

  const head = (
    <PageHead
      title={t.TitleChannels}
      actions={
        <Link className="btn btn-primary" to="/channels/new">
          {t.ChannelsNew}
        </Link>
      }
    />
  )

  if (error) {
    return (
      <Page wide>
        {head}
        <LoadFailed error={error} />
      </Page>
    )
  }

  const list = data ?? []

  return (
    <Page wide>
      {head}
      {flash && <p className={flash.ok ? 'flash ok' : 'flash bad'}>{flash.text}</p>}

      {loading && !data ? null : list.length === 0 ? (
        <div className="card">
          <Empty
            icon={<IconChannels size={20} />}
            title={t.ChannelsEmptyTitle}
            body={t.ChannelsEmptyBody}
            action={
              <Link className="btn btn-primary" to="/channels/new">
                {t.ChannelsEmptyAction}
              </Link>
            }
          />
        </div>
      ) : (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t.ChannelsTableName}</th>
                <th>{t.ChannelsTableType}</th>
                <th>{t.ChannelsTableState}</th>
                <th>{t.ChannelsTableSecrets}</th>
                <th className="right" />
              </tr>
            </thead>
            <tbody>
              {list.map((c) => (
                <tr key={c.name}>
                  <td>
                    <Link to={`/channels/${encodeURIComponent(c.name)}`}>{c.name}</Link>
                  </td>
                  <td>
                    <code>{c.type}</code>
                  </td>
                  <td>
                    {!c.enabled ? (
                      <Tag tone="off">{t.ChannelsTagDisabled}</Tag>
                    ) : c.live ? (
                      <Tag tone="on">{t.ChannelsTagLive}</Tag>
                    ) : (
                      <Tag tone="warn">{t.ChannelsTagNotLoaded}</Tag>
                    )}
                  </td>
                  {/* Which private parameters hold a value. Never the values:
                      the browser is not sent a credential it would only have to
                      send back. */}
                  <td className="muted">{c.secrets_set?.join(', ') || '—'}</td>
                  <td className="right">
                    <div className="actions">
                      {/* The only button on this row that proves a notification
                          arrives. Disabled when the channel has no destination
                          of its own: the test notification names no recipient,
                          so the delivery could only come back PERMANENT, and a
                          button whose only outcome is a failure the operator
                          did not cause is worse than one that says why it is
                          off. */}
                      <button
                        type="button"
                        className="btn btn-primary btn-sm"
                        disabled={busy === c.name || c.needs_recipients}
                        title={c.needs_recipients ? t.TestNotifyNeedsRecipient : undefined}
                        onClick={() => setNotifyChannel(c.name)}
                      >
                        {t.TestNotifyButton}
                      </button>
                      {/* A narrower question, and for five of the six channel
                          types it does not touch the network at all — but it
                          is the one that says whether the endpoint is
                          reachable, which is a different failure from a
                          message that did not arrive. */}
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        disabled={busy === c.name}
                        onClick={() => void onTest(c.name)}
                      >
                        {t.CommonTest}
                      </button>
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        disabled={busy === c.name}
                        onClick={() => void onResetBreaker(c.name)}
                      >
                        {t.ChannelsResetBreaker}
                      </button>
                      <button
                        type="button"
                        className="btn btn-danger btn-sm"
                        disabled={busy === c.name}
                        onClick={() => void onDelete(c.name)}
                      >
                        {t.CommonDelete}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <TestNotifyDialog channel={notifyChannel} onClose={() => setNotifyChannel(null)} />
    </Page>
  )
}
