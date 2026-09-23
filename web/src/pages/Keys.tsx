import { useCallback, useState } from 'react'

import { Copy } from '../components/Copy'
import { Dialog } from '../components/Dialog'
import { Empty, LoadFailed, Page, PageHead, Tag } from '../components/Page'
import { IconKeys } from '../components/icons'
import { APIError, keys } from '../lib/api'
import { fill, stamp } from '../lib/format'
import { useT } from '../lib/i18n'
import { useOnboarding } from '../lib/onboarding'
import { splitList } from '../lib/schema'
import { useAsync } from '../lib/useAsync'

/**
 * The API keys.
 *
 * ---------------------------------------------------------------------------
 * Two things on this page are shaped by the same fact: the key exists in the
 * create response and nowhere else. The store keeps a digest, so this page can
 * show a name, a state and a date, and nothing that would let anybody sign a
 * request.
 *
 * The create form is therefore not a <form>. A real submit would navigate, and
 * navigating is how the one view of that value would be lost. The token dialog
 * is likewise the only non-dismissible dialog in this interface — a stray click
 * on the backdrop cannot close it, because closing it discards the value and
 * the operator finds out when their integration returns 401.
 *
 * The list is refreshed when the dialog closes, not when the key is created,
 * for the same reason: a reload while the token is on screen is a reload that
 * throws it away.
 * ---------------------------------------------------------------------------
 */
export function Keys() {
  const t = useT()
  const { refresh: refreshSteps } = useOnboarding()
  const [flash, setFlash] = useState<{ text: string; ok: boolean } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const [name, setName] = useState('')
  const [recipients, setRecipients] = useState('')
  /** The plaintext token, while the dialog that shows it is open. */
  const [token, setToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  /** The key whose allow list is being edited, or null. */
  const [editing, setEditing] = useState<{ id: string; name: string; list: string } | null>(null)

  const load = useCallback((signal: AbortSignal) => keys.list(signal), [])
  const { data, error, loading, reload } = useAsync(load)

  async function act(id: string, run: () => Promise<string>) {
    setBusy(id)
    setFlash(null)
    try {
      setFlash({ text: await run(), ok: true })
      reload()
      refreshSteps()
    } catch (err) {
      setFlash({ text: err instanceof Error ? err.message : String(err), ok: false })
    } finally {
      setBusy(null)
    }
  }

  async function onCreate() {
    const trimmed = name.trim()
    if (!trimmed) {
      // The name is what the audit trail shows, so an unnamed key is one
      // nobody can account for later.
      setFlash({ text: t.Script.KeyNameRequired, ok: false })
      return
    }

    setBusy('__new__')
    setFlash(null)
    try {
      const created = await keys.create({ name: trimmed, allowed_recipients: splitList(recipients) })
      setName('')
      setRecipients('')
      setCopied(false)
      setToken(created.token)
    } catch (err) {
      setFlash({
        text: err instanceof APIError ? err.message : String(err),
        ok: false,
      })
    } finally {
      setBusy(null)
    }
  }

  async function onCopyToken() {
    if (!token) return
    try {
      await navigator.clipboard.writeText(token)
      setCopied(true)
    } catch {
      // The clipboard API needs a secure context, so a deployment reached over
      // plain HTTP cannot use it. The token is on screen and selectable, which
      // is the fallback — and claiming success here would be a lie the operator
      // discovers when they paste nothing.
      setCopied(false)
    }
  }

  function onTokenClosed() {
    setToken(null)
    // Now that the value is out of the way, the list can be re-read.
    reload()
    refreshSteps()
  }

  async function onSaveRecipients() {
    if (!editing) return
    const id = editing.id
    setBusy(id)
    try {
      // An empty box is sent as an empty list, which the server reads as "this
      // key may address nobody" — the same thing it means on the create form.
      // Sending nothing at all would mean "leave it alone", which is the one
      // reading an operator clearing the box cannot have intended.
      await keys.update(id, { allowed_recipients: splitList(editing.list) })
      setEditing(null)
      setFlash({ text: fill(t.Script.KeyRecipientsSaved, editing.name), ok: true })
      reload()
    } catch (err) {
      setFlash({ text: err instanceof Error ? err.message : String(err), ok: false })
    } finally {
      setBusy(null)
    }
  }

  const list = data ?? []
  const configured = list.filter((k) => k.source !== 'database').length

  return (
    <Page wide>
      <PageHead title={t.TitleKeys} description={t.KeysIntro} />

      <div className="card" style={{ marginBottom: 'var(--s5)' }}>
        <div className="grid" style={{ gridTemplateColumns: '2fr 2fr auto', alignItems: 'end' }}>
          <div className="field">
            <label htmlFor="key-name">{t.KeysNameLabel}</label>
            <input
              id="key-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t.KeysNameHint}
              autoComplete="off"
            />
            <p className="desc">{t.KeysNameDesc}</p>
          </div>

          {/* The one field here that decides what a key can reach beyond the
              destinations an operator already configured. Empty is the safe
              default and stays that way: a key that only fires alerts never
              needs it. */}
          <div className="field">
            <label htmlFor="key-recipients">{t.KeysRecipientsLabel}</label>
            <input
              id="key-recipients"
              value={recipients}
              onChange={(e) => setRecipients(e.target.value)}
              placeholder={t.KeysRecipientsHint}
              autoComplete="off"
            />
            <p className="desc">{t.KeysRecipientsDesc}</p>
          </div>

          <div className="field" style={{ paddingBottom: 22 }}>
            <button
              type="button"
              className="btn btn-primary"
              disabled={busy === '__new__'}
              onClick={() => void onCreate()}
            >
              {t.KeysCreateButton}
            </button>
          </div>
        </div>
      </div>

      {configured > 0 && (
        <p className="muted small">{fill(t.KeysConfiguredNotice, configured)}</p>
      )}

      {flash && <p className={flash.ok ? 'flash ok' : 'flash bad'}>{flash.text}</p>}
      {error && <LoadFailed error={error} />}

      {!error &&
        (loading && !data ? null : list.length === 0 ? (
          <div className="card">
            <Empty
              icon={<IconKeys size={20} />}
              title={t.KeysEmptyTitle}
              body={<Copy field="KeysEmptyBody" />}
            />
          </div>
        ) : (
          <div className="card table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t.KeysTableName}</th>
                  <th>{t.KeysTableSource}</th>
                  <th>{t.KeysTableStatus}</th>
                  <th>{t.KeysTableCreated}</th>
                  <th>{t.KeysTableLastUsed}</th>
                  <th>{t.KeysTableRecipients}</th>
                  <th className="right" />
                </tr>
              </thead>
              <tbody>
                {list.map((k) => (
                  <tr key={k.id}>
                    <td>{k.name}</td>
                    {/* The source column is where a key came from, in the
                        reader's language for the case that matters: a key
                        pinned by the configuration file cannot be edited here,
                        and saying so in the row is what stops somebody looking
                        for its delete button. */}
                    <td>
                      {k.source === 'configuration' ? (
                        <Tag>{t.CommonSetInConfig}</Tag>
                      ) : (
                        <Tag mono>{k.source}</Tag>
                      )}
                    </td>
                    <td>
                      <Tag tone={k.enabled ? 'on' : 'off'}>
                        {k.enabled ? t.CommonEnabled : t.CommonDisabled}
                      </Tag>
                    </td>
                    <td className="mono small">{stamp(k.created_at)}</td>
                    <td className="muted small">
                      {k.last_used_at ? stamp(k.last_used_at) : t.CommonNever}
                    </td>
                    {/* The default — a key that may address nobody — is spelled
                        out rather than left blank: an empty cell reads like a
                        value nobody filled in, and this one is a decision. */}
                    <td className="muted small">
                      {k.allowed_recipients?.length
                        ? k.allowed_recipients.join(', ')
                        : t.KeysRecipientsNone}
                    </td>
                    <td className="right">
                      <div className="actions">
                        {/* Only a key in the database can be changed here. The
                            ones from the configuration file are rendered as
                            text with no buttons rather than as disabled ones:
                            there is no permission to grant, and a greyed-out
                            button invites somebody to look for one. */}
                        {k.source === 'database' && (
                          <>
                            <button
                              type="button"
                              className="btn btn-ghost btn-sm"
                              disabled={busy === k.id}
                              onClick={() =>
                                void act(k.id, async () => {
                                  await keys.update(k.id, { enabled: !k.enabled })
                                  return fill(
                                    k.enabled ? t.Script.KeyStateDisabled : t.Script.KeyStateEnabled,
                                    k.name,
                                  )
                                })
                              }
                            >
                              {k.enabled ? t.CommonDisable : t.CommonEnable}
                            </button>
                            <button
                              type="button"
                              className="btn btn-ghost btn-sm"
                              disabled={busy === k.id}
                              onClick={() =>
                                setEditing({
                                  id: k.id,
                                  name: k.name,
                                  list: (k.allowed_recipients ?? []).join(', '),
                                })
                              }
                            >
                              {t.KeysRecipientsEdit}
                            </button>
                            <button
                              type="button"
                              className="btn btn-danger btn-sm"
                              disabled={busy === k.id}
                              onClick={() => {
                                if (!window.confirm(fill(t.Script.ConfirmDeleteKey, k.name))) return
                                void act(k.id, async () => {
                                  await keys.remove(k.id)
                                  return fill(t.Script.DeletedFlash, k.name)
                                })
                              }}
                            >
                              {t.CommonDelete}
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}

      {/* The token, once. Not dismissible: this is the only view of a value
          that is never stored, and a stray click outside must not be what
          discards it. */}
      <Dialog open={token !== null} onClose={onTokenClosed} title={t.KeysDialogHeading} dismissible={false}>
        <p className="muted small">{t.KeysDialogBody}</p>
        <pre className="block">{token}</pre>
        <div className="row dialog-actions">
          <button type="button" className="btn btn-primary" onClick={() => void onCopyToken()}>
            {copied ? t.CommonDone : t.KeysDialogCopy}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onTokenClosed}>
            {t.KeysDialogDone}
          </button>
        </div>
      </Dialog>

      {/* Editing the allow list of a key that already exists. The row's button
          carries the key's name and its current list, so the dialog never has
          to fetch them and the operator never edits a key they did not mean
          to. */}
      <Dialog
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={
          <>
            {t.KeysRecipientsHeading} — <code>{editing?.name}</code>
          </>
        }
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void onSaveRecipients()
          }}
        >
          <div className="field">
            <label htmlFor="key-recipients-edit">{t.KeysRecipientsLabel}</label>
            <input
              id="key-recipients-edit"
              value={editing?.list ?? ''}
              onChange={(e) => setEditing((prev) => (prev ? { ...prev, list: e.target.value } : prev))}
              placeholder={t.KeysRecipientsHint}
              autoComplete="off"
            />
            <p className="desc">{t.KeysRecipientsDesc}</p>
          </div>
          <div className="row dialog-actions">
            <button type="submit" className="btn btn-primary">
              {t.CommonSave}
            </button>
            <button type="button" className="btn btn-ghost" onClick={() => setEditing(null)}>
              {t.CommonCancel}
            </button>
          </div>
        </form>
      </Dialog>
    </Page>
  )
}
