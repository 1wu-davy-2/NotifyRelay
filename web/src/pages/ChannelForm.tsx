import { useCallback, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, type NavigateFunction } from 'react-router-dom'

import { Empty, Page, PageHead } from '../components/Page'
import { IconChannels } from '../components/icons'
import { APIError, channels, type ChannelSaveBody, type ChannelTestResult } from '../lib/api'
import { fill } from '../lib/format'
import { useT } from '../lib/i18n'
import type { Messages } from '../lib/messages'
import { useOnboarding } from '../lib/onboarding'
import {
  applies,
  collect,
  initialValues,
  NEW_CHANNEL,
  requiredNow,
  type ChannelFormData,
  type FieldValues,
  type FieldView,
} from '../lib/schema'
import { useAsync } from '../lib/useAsync'

/** The five quota fields, in the order the form shows them. */
const QUOTA_FIELDS = ['per_second', 'per_minute', 'per_hour', 'per_day', 'per_month'] as const

/**
 * The channel form.
 *
 * ---------------------------------------------------------------------------
 * Generated, not written.
 *
 * Every field on this page comes from GET /api/channel-form, which builds them
 * with the same function that renders the server-side form. Adding a parameter
 * to a channel adds it here; this file does not change. That is the property
 * the schema exists for, and it is worth more on the client than it was on the
 * server — a form built from a schema cannot fall behind the schema.
 *
 * What this file does own is the two things that belong to the browser: whether
 * a conditional field is currently on screen, and turning the controls back
 * into a request body. Both live in web/src/lib/schema.ts, next to the
 * explanation of why the rest is not there.
 * ---------------------------------------------------------------------------
 */
export function ChannelForm() {
  const t = useT()
  const navigate = useNavigate()
  const { refresh: refreshSteps } = useOnboarding()

  const params = useParams<{ name?: string }>()
  const editing = params.name ?? NEW_CHANNEL
  const isNew = editing === NEW_CHANNEL

  const load = useCallback(
    (signal: AbortSignal) => channels.form(isNew ? undefined : editing, signal),
    [editing, isNew],
  )
  const { data, error, loading } = useAsync(load)

  if (error) {
    return (
      <Page wide>
        <PageHead title={t.TitleChannels} />
        <div className="card">
          <Empty title={t.TitleError} body={error.message} />
        </div>
      </Page>
    )
  }

  if (loading && !data) return null
  if (!data) return null

  if (data.missing) {
    // A link to a channel that is not there any more. Rendering an empty form
    // headed "Edit X" would be a form that looks like an edit and is a create,
    // which is the kind of thing you find out about afterwards.
    return (
      <Page wide>
        <PageHead title={t.TitleChannels} />
        <div className="card">
          <Empty
            icon={<IconChannels size={20} />}
            title={t.ChannelGoneHead}
            body={fill(t.ChannelGoneBody, editing)}
            action={
              <Link className="btn btn-primary" to="/channels/new">
                {t.ChannelsNew}
              </Link>
            }
          />
        </div>
      </Page>
    )
  }

  // Keyed on the editing target so that navigating from one channel to another
  // rebuilds the state rather than keeping the previous channel's values.
  return <Form key={editing} t={t} data={data} editing={editing} isNew={isNew} navigate={navigate} refreshSteps={refreshSteps} />
}

function Form({
  t,
  data,
  editing,
  isNew,
  navigate,
  refreshSteps,
}: {
  t: Messages
  data: ChannelFormData
  editing: string
  isNew: boolean
  navigate: NavigateFunction
  refreshSteps: () => void
}) {
  // The name is fixed once the channel exists: it is the identity everything
  // else refers to — the API target, the SMTP local part, the audit trail — and
  // renaming it here would be a delete and a create wearing one button.
  const [name, setName] = useState(isNew ? '' : editing)
  const [type, setType] = useState(data.edit_type ?? '')
  const [enabled, setEnabled] = useState(data.edit_enabled)

  // One set of values per type, not one for the active type. Switching type in
  // the picker and switching back should not discard what was typed — the
  // server-rendered form keeps all the blocks in the DOM and hides the ones
  // that do not apply, which has the same effect.
  const [values, setValues] = useState<Record<string, FieldValues>>(() =>
    Object.fromEntries(data.forms.map((f) => [f.type, initialValues(f.fields)])),
  )
  const [clears, setClears] = useState<Record<string, Record<string, boolean>>>({})

  const [quota, setQuota] = useState<Record<string, number>>(() => {
    const out: Record<string, number> = {}
    for (const field of QUOTA_FIELDS) out[field] = data.edit_quota?.[field] ?? 0
    return out
  })

  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [flash, setFlash] = useState<{ text: string; ok: boolean } | null>(null)
  const [busy, setBusy] = useState(false)

  const active = useMemo(() => data.forms.find((f) => f.type === type), [data.forms, type])
  const activeValues = active ? (values[active.type] ?? {}) : {}

  function setValue(field: string, value: string | boolean) {
    if (!active) return
    setValues((prev) => ({ ...prev, [active.type]: { ...prev[active.type], [field]: value } }))
    // A field that has just been corrected should stop showing the complaint
    // about its old value. Leaving it there until the next save is how a form
    // tells somebody a box is still wrong when it is not.
    setFieldErrors((prev) => {
      if (!(field in prev)) return prev
      const next = { ...prev }
      delete next[field]
      return next
    })
  }

  function setClear(field: string, value: boolean) {
    if (!active) return
    setClears((prev) => ({ ...prev, [active.type]: { ...prev[active.type], [field]: value } }))
  }

  function body(replace = false): ChannelSaveBody {
    return {
      name: name.trim(),
      type,
      enabled,
      config: active ? collect(active.fields, activeValues, clears[active.type] ?? {}) : {},
      quota,
      editing,
      ...(replace ? { replace: true } : {}),
    }
  }

  async function save(replace = false) {
    setBusy(true)
    setFlash(null)
    setFieldErrors({})

    try {
      const saved = await channels.save(body(replace))
      if (saved.live === false) {
        // Saved, and the running service would not load it. That is a result
        // rather than a failure, and it is the one that matters: the channel
        // exists, is enabled, and is not delivering.
        setFlash({ text: fill(t.Script.SavedButNotLive, saved.name ?? name), ok: false })
        return
      }
      refreshSteps()
      navigate('/channels')
    } catch (err) {
      onSaveFailed(err, replace)
    } finally {
      setBusy(false)
    }
  }

  function onSaveFailed(err: unknown, alreadyReplaced: boolean) {
    if (!(err instanceof APIError)) {
      setFlash({ text: String(err), ok: false })
      return
    }

    // A name collision is a question, not a failure. Saving would replace a
    // channel that may be delivering right now, and the operator asked to
    // create a new one — so they are told what the name collides with, and
    // asked again with the answer attached.
    if (err.code === 'name_taken' && !alreadyReplaced) {
      if (window.confirm(`${err.message}\n\n${t.Script.ConfirmReplace}`)) {
        void save(true)
      }
      return
    }

    // A refusal that named parameters goes under the boxes it is about. What
    // could not be placed — an unknown key, or a pair that is only wrong
    // together — is listed above the form, because the reason the save was
    // refused has to appear somewhere.
    const placed = err.fields ?? {}
    const onScreen = new Set(
      (active?.fields ?? []).filter((f) => applies(f, activeValues)).map((f) => f.name),
    )
    const unplaced = Object.entries(placed)
      .filter(([field]) => !onScreen.has(field))
      .map(([, message]) => message)

    setFieldErrors(Object.fromEntries(Object.entries(placed).filter(([f]) => onScreen.has(f))))
    setFlash({
      text: unplaced.length > 0 ? unplaced.join('\n') : t.Script.FixMarkedFields,
      ok: false,
    })
  }

  async function testConnection() {
    const target = name.trim()
    if (!target) {
      // There is nothing to reach yet. Saying so beats a 404 dressed up as a
      // connectivity failure.
      setFlash({ text: t.Script.SaveBeforeTest, ok: false })
      return
    }

    setBusy(true)
    setFlash(null)
    try {
      const res: ChannelTestResult = await channels.test(target)
      setFlash({
        text: res.ok
          ? fill(t.Script.TestOK, res.detail ?? res.class)
          : fill(t.Script.TestFailed, res.class, res.error ?? res.detail ?? ''),
        ok: res.ok,
      })
    } catch (err) {
      setFlash({ text: err instanceof Error ? err.message : String(err), ok: false })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Page wide>
      <div className="crumbs">
        <Link to="/channels">{t.TitleChannels}</Link>
        <span className="sep"> / </span>
        <b>{isNew ? t.ChannelsNew : fill(t.ChannelFormEditHeading, editing)}</b>
      </div>

      <PageHead title={isNew ? t.ChannelsNew : fill(t.ChannelFormEditHeading, editing)} />

      <form
        className="card"
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
      >
        <div className="grid">
          <div className="field">
            <label htmlFor="channel-name">{t.ChannelFormNameLabel}</label>
            <input
              id="channel-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              readOnly={!isNew}
              required
              autoComplete="off"
            />
            <p className="desc">{t.ChannelFormNameDesc}</p>
          </div>

          <div className="field">
            <label htmlFor="channel-type">{t.ChannelFormTypeLabel}</label>
            <select
              id="channel-type"
              value={type}
              onChange={(e) => setType(e.target.value)}
              required
            >
              {/* A placeholder rather than letting the browser pick the first
                  entry. Without it, "New channel" opens on whichever type sorts
                  first, and the fastest way through the form is to accept a
                  choice nobody made. */}
              {type === '' && (
                <option value="" disabled>
                  {t.ChannelFormTypeHint}
                </option>
              )}
              {data.forms.map((f) => (
                <option key={f.type} value={f.type}>
                  {f.label}
                </option>
              ))}
            </select>
            <p className="desc">{t.ChannelFormTypeDesc}</p>
          </div>

          {/* A bare checkbox under its own label, which is what the
              server-rendered form has. A second label beside the box said
              "enabled" underneath the word "enabled", which reads as a
              rendering fault rather than as emphasis. */}
          <div className="field">
            <span className="label">{t.ChannelFormEnabled}</span>
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
          </div>
        </div>

        <h3 className="section">{t.ChannelFormConfigHead}</h3>
        {active ? (
          <div className="grid">
            {active.fields.map((field) => (
              <Field
                key={field.name}
                t={t}
                field={field}
                values={activeValues}
                clear={Boolean(clears[active.type]?.[field.name])}
                error={fieldErrors[field.name]}
                onValue={(v) => setValue(field.name, v)}
                onClear={(v) => setClear(field.name, v)}
              />
            ))}
          </div>
        ) : (
          <p className="desc" style={{ padding: '0 22px 22px' }}>
            {t.ChannelFormTypeHint}
          </p>
        )}

        <h3 className="section">{t.ChannelFormQuotaHead}</h3>
        <p className="desc section-desc">{t.ChannelFormQuotaDesc}</p>
        <div className="grid">
          {QUOTA_FIELDS.map((field) => (
            <div className="field" key={field}>
              <label htmlFor={`q-${field}`}>{field}</label>
              <input
                id={`q-${field}`}
                type="number"
                min={0}
                value={quota[field] ?? 0}
                onChange={(e) => setQuota((prev) => ({ ...prev, [field]: Number(e.target.value) || 0 }))}
              />
            </div>
          ))}
        </div>

        {flash && (
          <p className={flash.ok ? 'flash ok inset' : 'flash bad inset'}>{flash.text}</p>
        )}

        <div className="form-foot">
          <div className="row">
            <button type="submit" className="btn btn-primary" disabled={busy}>
              {t.CommonSave}
            </button>
            <Link className="btn btn-ghost" to="/channels">
              {t.CommonCancel}
            </Link>
          </div>
          <button
            type="button"
            className="btn btn-ghost"
            onClick={() => void testConnection()}
            disabled={busy}
          >
            {t.ChannelFormTestConn}
          </button>
        </div>
      </form>
    </Page>
  )
}

/** One generated control. */
function Field({
  t,
  field,
  values,
  clear,
  error,
  onValue,
  onClear,
}: {
  t: Messages
  field: FieldView
  values: FieldValues
  clear: boolean
  error?: string
  onValue: (value: string | boolean) => void
  onClear: (value: boolean) => void
}) {
  // The condition is evaluated on every render rather than kept in state: the
  // field it depends on is in this same state object, so the two cannot
  // disagree.
  if (!applies(field, values)) return null

  const required = requiredNow(field, values)
  const id = `f-${field.name}`
  const value = values[field.name]

  return (
    <div className="field">
      <label htmlFor={id}>
        {field.label}
        {required && (
          <span className="req" title={t.CommonRequired}>
            *
          </span>
        )}
      </label>

      {field.kind === 'checkbox' ? (
        <label className="check">
          <input
            id={id}
            type="checkbox"
            checked={value === true}
            onChange={(e) => onValue(e.target.checked)}
          />
          {field.label}
        </label>
      ) : field.kind === 'select' ? (
        <select id={id} value={typeof value === 'string' ? value : ''} onChange={(e) => onValue(e.target.value)} required={required}>
          {(field.options ?? []).map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      ) : field.kind === 'password' ? (
        <>
          {/* The value is never rendered, whatever is stored: the browser is
              told that a credential exists and nothing more. Blank means "leave
              it alone" unless Clear is ticked. */}
          <input
            id={id}
            type="password"
            value={typeof value === 'string' ? value : ''}
            onChange={(e) => onValue(e.target.value)}
            autoComplete="new-password"
            placeholder={field.is_set ? t.ChannelFormSecretHint : undefined}
            required={required && !field.is_set}
          />
          {field.is_set && (
            <label className="check inline">
              <input type="checkbox" checked={clear} onChange={(e) => onClear(e.target.checked)} />
              {t.ChannelFormSecretClear}
            </label>
          )}
        </>
      ) : (
        <input
          id={id}
          type={field.kind === 'number' ? 'number' : 'text'}
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onValue(e.target.value)}
          min={field.min}
          max={field.max}
          required={required}
          autoComplete="off"
        />
      )}

      {/* A list parameter arrives as comma-separated text and this form splits
          it. The schema's own description does not say so, which leaves the
          convention undocumented on the page — the operator finds out by typing
          "a, b" and getting one address containing a comma. */}
      {field.kind === 'list' && <p className="desc">{t.ChannelFormListHint}</p>}
      {field.description && <p className="desc">{field.description}</p>}
      {error && <p className="err">{error}</p>}
    </div>
  )
}
