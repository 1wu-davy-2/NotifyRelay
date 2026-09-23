import { useCallback } from 'react'

import { Empty, LoadFailed, Page, PageHead, Tag } from '../components/Page'
import { IconAudit } from '../components/icons'
import { audit } from '../lib/api'
import { since, stamp } from '../lib/format'
import { useT } from '../lib/i18n'
import { useAsync } from '../lib/useAsync'

/**
 * The audit trail.
 *
 * What this interface did that had an effect, kept longer than the deliveries
 * it is about. The point of it is answering "what happened to that message"
 * long after the message is gone, which is why the actions are shown with their
 * actor and target rather than folded into a count.
 *
 * Three states, not two. "No trail configured" and "nothing has happened yet"
 * both render as an empty table and they mean opposite things: the first is a
 * fact about the deployment that somebody should act on, the second is a fact
 * about the afternoon. The server sends the flag that tells them apart.
 */
export function Audit() {
  const t = useT()
  const load = useCallback((signal: AbortSignal) => audit.list(signal), [])
  const { data, error, loading } = useAsync(load)

  return (
    <Page wide>
      <PageHead title={t.TitleAudit} description={t.AuditIntro} />

      {error && <LoadFailed error={error} />}

      {!error &&
        (loading && !data ? null : !data?.enabled ? (
          <div className="card">
            <Empty
              icon={<IconAudit size={20} />}
              title={t.AuditDisabledHead}
              body={t.AuditDisabledBody}
            />
          </div>
        ) : data.actions.length === 0 ? (
          <div className="card">
            <Empty
              icon={<IconAudit size={20} />}
              title={t.AuditEmptyHead}
              body={t.AuditEmptyBody}
            />
          </div>
        ) : (
          <div className="card table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t.AuditTableWhen}</th>
                  <th>{t.AuditTableWho}</th>
                  <th>{t.AuditTableAction}</th>
                  <th>{t.AuditTableChannel}</th>
                  <th>{t.AuditTableDetail}</th>
                </tr>
              </thead>
              <tbody>
                {data.actions.map((a, i) => (
                  <tr key={`${a.at}-${a.action}-${a.target ?? ''}-${i}`}>
                    {/* The exact timestamp in the title: "3 minutes ago" is
                        what somebody scanning wants, and the second is what
                        somebody correlating with a log needs. */}
                    <td title={stamp(a.at)}>{since(t, a.at)}</td>
                    <td>{a.actor}</td>
                    <td>
                      <Tag mono>{a.action}</Tag>
                    </td>
                    <td>{a.target}</td>
                    <td>
                      <Tag mono>{a.detail}</Tag>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
    </Page>
  )
}
