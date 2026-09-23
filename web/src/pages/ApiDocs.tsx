import { useCallback, useState } from 'react'

import { Copy } from '../components/Copy'
import { LoadFailed, Page, PageHead } from '../components/Page'
import { apiDocs } from '../lib/api'
import { useT } from '../lib/i18n'
import { useAsync } from '../lib/useAsync'

/**
 * The API reference.
 *
 * It exists because the person who has to call this service is often not the
 * person who deployed it, and the answer to "how do I send one of these from
 * Python" should not be "read the Go source". The samples are real files under
 * internal/admin/samples/, in both languages, and a test holds the two copies
 * to being the same program — see the note in apidocs.go.
 *
 * Everything on this page comes from the server. Nothing here is written twice.
 */
export function ApiDocs() {
  const t = useT()
  const [active, setActive] = useState<string | null>(null)
  const [copied, setCopied] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => apiDocs.get(signal), [])
  const { data, error, loading } = useAsync(load)

  async function copy(id: string, body: string) {
    try {
      await navigator.clipboard.writeText(body)
      setCopied(id)
      // Cleared after a moment: a button that stays on "copied" is one that
      // stops saying anything the second time somebody looks at it.
      window.setTimeout(() => setCopied((c) => (c === id ? null : c)), 2000)
    } catch {
      // The clipboard API needs a secure context, so a page reached over plain
      // HTTP cannot use it — which is exactly the deployment the warning below
      // is about. Selecting the text is the fallback, and the <pre> is
      // selectable, so there is nothing to do but not claim success.
      setCopied(null)
    }
  }

  if (error) {
    return (
      <Page wide>
        <PageHead title={t.TitleAPI} />
        <LoadFailed error={error} />
      </Page>
    )
  }

  if (loading && !data) return null
  if (!data) return null

  const current = active ?? data.samples[0]?.id ?? null

  return (
    <Page wide className="docs">
      <PageHead title={t.TitleAPI} description={<Copy field="APIDocsIntro" />} />

      {/* The two facts a caller needs before anything else, and the caveats
          that go with them. The address is the one this operator reached the
          service on, and the token cannot be recovered from here — both are
          obvious once you know them and expensive when you assume the other. */}
      <div className="card creds">
        <div className="grid">
          <div className="field">
            <div className="label">{t.APIDocsBaseURL}</div>
            <code>{data.base_url}</code>
          </div>
          <div className="field">
            <div className="label">{t.APIDocsAuth}</div>
            <code>Authorization: Bearer &lt;token&gt;</code>
          </div>
        </div>
        <div className="foot">
          <p>{t.APIDocsBaseURLNote}</p>
          <p><Copy field="APIDocsTokenNote" /></p>
        </div>
      </div>

      {/* A page reached over plain HTTP is about to hand the reader a token to
          put in a script. On the page rather than in a document nobody opens,
          and not shown behind a TLS-terminating proxy, where the operator's own
          connection really is encrypted. */}
      {!data.secure && <p className="flash bad"><Copy field="APIDocsPlainHTTP" /></p>}

      <h2>{t.APIDocsExamplesHeading}</h2>
      <p className="lede">
        <Copy field="APIDocsExamplesIntro" />
      </p>

      <div className="tabs" role="tablist">
        {data.samples.map((s) => (
          <button
            key={s.id}
            type="button"
            role="tab"
            aria-selected={s.id === current}
            className={s.id === current ? 'on' : undefined}
            onClick={() => setActive(s.id)}
          >
            {s.label}
          </button>
        ))}
      </div>

      {data.samples
        .filter((s) => s.id === current)
        .map((s) => (
          <div key={s.id}>
            <div className="sample-head">
              <span className="muted small">{s.note}</span>
              <span className="spacer" />
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => void copy(s.id, s.body)}
              >
                {copied === s.id ? t.CommonDone : t.APIDocsCopy}
              </button>
            </div>
            <pre className="block">{s.body}</pre>
          </div>
        ))}

      {/* The one part of the request the samples do not show. They all send to
          a channel's own destination, which is what an alert needs and what
          most callers want. Transactional mail names its recipient per request,
          and that is a different shape with rules of its own. */}
      <h2>{t.APIDocsRecipientsHeading}</h2>
      <p className="lede">
        <Copy field="APIDocsRecipientsIntro" />
      </p>
      <pre className="block">{t.APIDocsRecipientsExampleTo}</pre>
      <p className="lede">
        <Copy field="APIDocsRecipientsSame" />
      </p>
      <pre className="block">{t.APIDocsRecipientsExampleURL}</pre>
      <div className="card notes">
        <ul className="muted">
          <li><Copy field="APIDocsRecipientsReplace" /></li>
          <li><Copy field="APIDocsRecipientsAllowlist" /></li>
          <li><Copy field="APIDocsRecipientsExclusive" /></li>
        </ul>
      </div>

      <h2>{t.APIDocsEndpointsHead}</h2>
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t.APIDocsTableMethod}</th>
              <th>{t.APIDocsTablePath}</th>
              <th>{t.APIDocsTableAuth}</th>
              <th>{t.APIDocsTablePurpose}</th>
            </tr>
          </thead>
          <tbody>
            {data.endpoints.map((e) => (
              <tr key={e.method + e.path}>
                <td>
                  <code>{e.method}</code>
                </td>
                <td>
                  <code>{e.path}</code>
                </td>
                <td>
                  {e.auth ? (
                    <code>{e.auth}</code>
                  ) : (
                    <span className="muted">{t.APIDocsAuthNone}</span>
                  )}
                </td>
                <td>{e.purpose}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <h2>{t.APIDocsErrorsHeading}</h2>
      <p className="lede">
        <Copy field="APIDocsErrorBodyNote" />
      </p>
      <div className="card table-wrap">
        <table>
          <thead>
            <tr>
              <th>
                <code>error</code>
              </th>
              <th>{t.APIDocsTableStatus}</th>
              <th>{t.APIDocsTableMeaning}</th>
            </tr>
          </thead>
          <tbody>
            {data.errors.map((e) => (
              <tr key={e.code}>
                <td>
                  <code>{e.code}</code>
                </td>
                <td>{e.status}</td>
                <td>{e.meaning}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* The two mistakes that are not visible from the endpoint table: the
          same enum spelled two ways depending on the endpoint, and a status
          that reads like a failure and is not one. */}
      <h2>{t.APIDocsGotchasHeading}</h2>
      <div className="card notes">
        <ul className="muted">
          <li><Copy field="APIDocsGotchaClassCase" /></li>
          <li><Copy field="APIDocsGotchaNotAtt" /></li>
        </ul>
      </div>
    </Page>
  )
}
