import { useCallback, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { Empty, LoadFailed, Page, PageHead, Tag } from '../components/Page'
import { IconDeliveries } from '../components/icons'
import { deliveries, type DeliveryQuery } from '../lib/api'
import { fill, since, stamp } from '../lib/format'
import { useT } from '../lib/i18n'
import { STATUSES, statusLabel, statusTone } from '../lib/status'
import { useAsync } from '../lib/useAsync'

const DEFAULT_LIMIT = 50

/**
 * The delivery list.
 *
 * The filters live in the URL rather than in component state, which is what
 * makes a filtered view something an operator can bookmark, reload, or paste to
 * somebody else. It is also what the server-rendered page does, and the two
 * have to agree: a link to /admin/deliveries?status=failed should show the same
 * rows whichever interface opens it.
 */
export function Deliveries() {
  const t = useT()
  const [params, setParams] = useSearchParams()
  const [flash, setFlash] = useState<{ text: string; ok: boolean } | null>(null)

  const query: DeliveryQuery = {
    status: params.get('status') ?? '',
    target: params.get('target') ?? '',
    request_id: params.get('request_id') ?? '',
    limit: Number(params.get('limit')) || DEFAULT_LIMIT,
    offset: Number(params.get('offset')) || 0,
  }

  // The filter is part of the identity of this request, so it is part of the
  // dependency list: changing it re-runs the loader rather than leaving the
  // previous rows on screen.
  const key = params.toString()
  const load = useCallback(
    (signal: AbortSignal) => deliveries.list(query, signal),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [key],
  )
  const { data, error, loading, reload } = useAsync(load)

  const statsLoad = useCallback((signal: AbortSignal) => deliveries.stats(signal), [])
  const { data: stats } = useAsync(statsLoad)

  /** Applies the filter form, dropping empty fields from the URL. */
  function onFilter(form: FormData) {
    const next = new URLSearchParams()
    for (const name of ['status', 'target', 'request_id']) {
      const value = String(form.get(name) ?? '').trim()
      if (value) next.set(name, value)
    }
    const limit = String(form.get('limit') ?? '').trim()
    if (limit && limit !== String(DEFAULT_LIMIT)) next.set('limit', limit)
    setParams(next)
  }

  function onClear() {
    setParams(new URLSearchParams())
  }

  async function onReplay(id: string) {
    if (!window.confirm(fill(t.Script.ConfirmReplay, id))) return
    try {
      await deliveries.replay(id)
      setFlash({ text: fill(t.Script.ReplayedFlash, id), ok: true })
      reload()
    } catch (err) {
      setFlash({ text: err instanceof Error ? err.message : String(err), ok: false })
    }
  }

  const filtered = Boolean(query.status || query.target || query.request_id)

  function pager(direction: 'prev' | 'next') {
    const next = new URLSearchParams(params)
    const limit = query.limit ?? DEFAULT_LIMIT
    const offset = query.offset ?? 0
    next.set('offset', String(direction === 'next' ? offset + limit : Math.max(0, offset - limit)))
    return `/deliveries?${next.toString()}`
  }

  return (
    <Page wide>
      <PageHead title={t.TitleDeliveries} />

      {/* The counters as cards rather than one sentence. They are read at a
          glance and compared with each other, and each label is the same word
          the status tag below uses, so the two cannot drift apart. */}
      <div className="stats">
        <div className="card stat c1">
          <div className="num">{stats?.queued ?? 0}</div>
          <div className="lbl">{t.StatusQueued}</div>
        </div>
        <div className="card stat c2">
          <div className="num">{stats?.sending ?? 0}</div>
          <div className="lbl">{t.StatusSending}</div>
        </div>
        <div className="card stat c3">
          <div className="num">{stats?.sent ?? 0}</div>
          <div className="lbl">{t.StatusSent}</div>
        </div>
        <div className="card stat c4">
          <div className="num">{stats?.failed ?? 0}</div>
          <div className="lbl">{t.StatusFailed}</div>
        </div>
      </div>

      <form
        className="card toolbar"
        onSubmit={(e) => {
          e.preventDefault()
          onFilter(new FormData(e.currentTarget))
        }}
      >
        <label htmlFor="f-status">{t.DeliveriesFilterStatus}</label>
        <select id="f-status" name="status" defaultValue={query.status}>
          <option value="">{t.CommonAny}</option>
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {statusLabel(t, s)}
            </option>
          ))}
        </select>

        <label htmlFor="f-target">{t.DeliveriesFilterChannel}</label>
        <input
          id="f-target"
          name="target"
          defaultValue={query.target}
          placeholder={t.CommonAny}
        />

        <label htmlFor="f-request">{t.DeliveriesFilterRequestID}</label>
        <input
          id="f-request"
          name="request_id"
          defaultValue={query.request_id}
          placeholder={t.CommonAny}
        />

        <label htmlFor="f-limit">{t.DeliveriesFilterLimit}</label>
        <input
          id="f-limit"
          name="limit"
          type="number"
          min={1}
          max={500}
          defaultValue={query.limit}
          style={{ width: 80 }}
        />

        <button type="submit" className="btn btn-primary btn-sm">
          {t.CommonFilter}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onClear}>
          {t.CommonClear}
        </button>
      </form>

      {flash && <p className={flash.ok ? 'flash ok' : 'flash bad'}>{flash.text}</p>}
      {error && <LoadFailed error={error} />}

      {!error && (loading && !data ? null : (data ?? []).length === 0 ? (
        <div className="card">
          {/* A filter that matched nothing is a different fact from nothing
              having ever been sent, and the two used to render the same
              sentence — which sent the reader to the channel list when what
              they needed was to widen the filter they had just typed. */}
          {filtered ? (
            <Empty
              icon={<IconDeliveries size={20} />}
              title={t.DeliveriesNoMatchTitle}
              body={t.DeliveriesNoMatchBody}
              action={
                <button type="button" className="btn btn-ghost" onClick={onClear}>
                  {t.DeliveriesNoMatchAction}
                </button>
              }
            />
          ) : (
            <Empty
              icon={<IconDeliveries size={20} />}
              title={t.DeliveriesEmptyTitle}
              body={t.DeliveriesEmptyBody}
              action={
                <Link className="btn btn-primary" to="/channels">
                  {t.DeliveriesEmptyAction}
                </Link>
              }
            />
          )}
        </div>
      ) : (
        <>
          <div className="card table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t.DeliveriesTableCreated}</th>
                  <th>{t.DeliveriesTableChannel}</th>
                  <th>{t.DeliveriesTableStatus}</th>
                  <th>{t.DeliveriesTableClass}</th>
                  <th>{t.DeliveriesTableAttempts}</th>
                  <th>{t.DeliveriesTableLastError}</th>
                  <th className="right" />
                </tr>
              </thead>
              <tbody>
                {(data ?? []).map((d) => (
                  <tr key={d.id}>
                    <td title={stamp(d.created_at)}>{since(t, d.created_at)}</td>
                    <td>
                      <code>{d.target}</code> <span className="muted small">{d.channel_type}</span>
                    </td>
                    <td>
                      <Tag tone={statusTone(d.status)}>{statusLabel(t, d.status)}</Tag>
                    </td>
                    {/* The class the router settled on. Muted, because it is
                        the explanation and the status is the headline. */}
                    <td className="muted">{d.last_class}</td>
                    <td>{d.attempts}</td>
                    <td className="muted small">{d.last_error}</td>
                    <td className="right">
                      <div className="actions">
                        <Link className="btn btn-ghost btn-sm" to={`/deliveries/${encodeURIComponent(d.id)}`}>
                          {t.CommonDetail}
                        </Link>
                        {/* Replayable is the server's answer, not this page's
                            reading of `status`: it also depends on whether the
                            message body is still on disk, and a button offered
                            for a body that is gone is a button that fails. */}
                        {d.replayable && (
                          <button
                            type="button"
                            className="btn btn-ghost btn-sm"
                            onClick={() => void onReplay(d.id)}
                          >
                            {t.CommonReplay}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="row" style={{ marginTop: 'var(--s5)' }}>
            {query.offset! > 0 && (
              <Link className="btn btn-ghost btn-sm" to={pager('prev')}>
                {t.CommonNewer}
              </Link>
            )}
            {(data ?? []).length === query.limit && (
              <Link className="btn btn-ghost btn-sm" to={pager('next')}>
                {t.CommonOlder}
              </Link>
            )}
          </div>
        </>
      ))}
    </Page>
  )
}
