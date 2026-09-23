import { useCallback, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { Empty, LoadFailed, Page, PageHead, Tag } from '../components/Page'
import { IconDeliveries } from '../components/icons'
import { APIError, deliveries } from '../lib/api'
import { fill, since, stamp } from '../lib/format'
import { useT } from '../lib/i18n'
import { statusLabel, statusTone } from '../lib/status'
import { useAsync } from '../lib/useAsync'

/**
 * One delivery and everything that happened to it.
 *
 * This is where "did it work" is actually answered. Everywhere else — the
 * channel list's test button, the test-notification dialog — can only say that
 * a message was queued, because that is all that has happened by the time they
 * return. The attempt history is the record.
 *
 * An empty attempt history is not an empty page: it is the finding. The channel
 * was never called, which is a different problem from a call that failed, and
 * the empty state names the three things that hold a call back rather than
 * leaving a blank table.
 */
export function DeliveryDetail() {
  const t = useT()
  const { id = '' } = useParams<{ id: string }>()
  const [flash, setFlash] = useState<{ text: string; ok: boolean } | null>(null)

  const load = useCallback((signal: AbortSignal) => deliveries.get(id, signal), [id])
  const { data, error, loading, reload } = useAsync(load)

  async function onReplay() {
    if (!window.confirm(fill(t.Script.ConfirmReplay, id))) return
    try {
      await deliveries.replay(id)
      setFlash({ text: fill(t.Script.ReplayedFlash, id), ok: true })
      reload()
    } catch (err) {
      setFlash({
        text: err instanceof APIError ? err.message : String(err),
        ok: false,
      })
    }
  }

  if (error) {
    return (
      <Page wide>
        <PageHead title={t.TitleDelivery} />
        <LoadFailed error={error} />
      </Page>
    )
  }

  if (loading && !data) return null
  if (!data) return null

  const { delivery: d, attempts } = data

  return (
    <Page wide>
      <div className="crumbs">
        <Link to="/deliveries">{t.TitleDeliveries}</Link>
        <span className="sep"> / </span>
        <b className="mono">{d.id}</b>
      </div>

      <PageHead
        title={t.TitleDelivery}
        actions={
          <div className="row">
            <Link className="btn btn-ghost" to="/deliveries">
              {t.CommonBack}
            </Link>
            {d.replayable && (
              <button type="button" className="btn btn-primary" onClick={() => void onReplay()}>
                {t.CommonReplay}
              </button>
            )}
          </div>
        }
      />

      {flash && <p className={flash.ok ? 'flash ok' : 'flash bad'}>{flash.text}</p>}

      <dl className="facts card">
        <dt>{t.DeliveryFactID}</dt>
        <dd>
          <code>{d.id}</code>
        </dd>

        <dt>{t.DeliveryFactRequest}</dt>
        <dd>
          <code>{d.request_id}</code>
        </dd>

        <dt>{t.DeliveryFactChannel}</dt>
        <dd>
          <code>{d.target}</code> <span className="muted small">{d.channel_type}</span>
        </dd>

        <dt>{t.DeliveryFactStatus}</dt>
        <dd>
          <Tag tone={statusTone(d.status)}>{statusLabel(t, d.status)}</Tag>
        </dd>

        <dt>{t.DeliveryFactAttempts}</dt>
        <dd>{d.attempts}</dd>

        <dt>{t.DeliveryFactCreated}</dt>
        <dd>
          {stamp(d.created_at)} <span className="muted small">({since(t, d.created_at)})</span>
        </dd>

        {d.sent_at && (
          <>
            <dt>{t.DeliveryFactSent}</dt>
            <dd>{stamp(d.sent_at)}</dd>
          </>
        )}

        {d.next_attempt_at && (
          <>
            <dt>{t.DeliveryFactNextAttempt}</dt>
            <dd>{stamp(d.next_attempt_at)}</dd>
          </>
        )}

        {d.last_error && (
          <>
            <dt>{t.DeliveryFactLastError}</dt>
            <dd className="wrap">{d.last_error}</dd>
          </>
        )}
      </dl>

      {/* Why the button above is not there, in the two cases it is withheld.
          The dead-letter row and the message body have separate lifetimes, so a
          delivery can be dead-lettered and still not be replayable — and a page
          that simply omitted the button would leave the operator clicking
          Detail on a row that looks replayable and finding nothing to press. */}
      {!d.replayable && (
        <p className="muted small">
          {d.status === 'failed' ? t.DeliveryBodyExpired : t.DeliveryNotDeadLetter}
        </p>
      )}

      <h2 className="section">{t.DeliveryAttemptsHeading}</h2>

      {attempts.length === 0 ? (
        <div className="card">
          <Empty
            icon={<IconDeliveries size={20} />}
            title={t.DeliveryEmptyTitle}
            body={t.DeliveryEmptyBody}
            action={
              <Link className="btn btn-ghost" to="/api-docs">
                {t.BackToAPI}
              </Link>
            }
          />
        </div>
      ) : (
        <div className="card table-wrap">
          <table>
            <thead>
              <tr>
                <th>#</th>
                <th>{t.DeliveryAttemptWhen}</th>
                <th>{t.DeliveryAttemptClass}</th>
                <th>{t.DeliveryAttemptSkipped}</th>
                <th>ms</th>
                <th>{t.DeliveryAttemptDetail}</th>
                <th>{t.DeliveryAttemptError}</th>
              </tr>
            </thead>
            <tbody>
              {attempts.map((a) => (
                <tr key={a.attempt_no}>
                  <td>{a.attempt_no}</td>
                  <td title={stamp(a.created_at)}>{since(t, a.created_at)}</td>
                  {/* The class as it arrives: it is an identifier the API
                      reference documents, not a sentence to translate. */}
                  <td>
                    <Tag mono>{a.class}</Tag>
                  </td>
                  <td className="muted small">{a.skip_reason}</td>
                  <td>{a.elapsed_ms}</td>
                  <td className="small">{a.detail}</td>
                  <td className="small">{a.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Page>
  )
}
