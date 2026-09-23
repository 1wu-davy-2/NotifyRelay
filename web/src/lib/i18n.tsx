/**
 * The copy table, fetched once and shared.
 *
 * ---------------------------------------------------------------------------
 * The strings live in Go and are read from there, not duplicated here.
 *
 * internal/admin/i18n holds every sentence the interface says, in every
 * language it ships, and the server-rendered pages read it directly. The
 * client-side interface reads the same table over GET /admin/api/i18n, which is
 * the whole reason that endpoint exists.
 *
 * The alternative — a second table in TypeScript — is the one that looks
 * simpler and is not: it makes every copy change a two-place edit, and the two
 * places are maintained by people who will not always remember the second one.
 * The failure is a sentence that is right on the sign-in page and stale on the
 * channels page, which is not a bug anybody files.
 *
 * What it costs is a round trip before the first paint. It is one request, on
 * the order of thirty kilobytes compressed, once per page load, and the page
 * has nothing to say until it arrives anyway.
 * ---------------------------------------------------------------------------
 */

import { createContext, use, useEffect, useState, type ReactNode } from 'react'

import { request } from './api'
import type { I18nPayload, LangOption, Messages } from './messages'

interface I18n {
  /** The table for the current language. */
  t: Messages
  /** The language tag it is in. */
  lang: string
  /** Every language this build has, for the switcher. */
  langs: LangOption[]
}

const Context = createContext<I18n | null>(null)

/**
 * Loads the table and provides it.
 *
 * Children are not rendered until it arrives. A shell that rendered first and
 * filled in the words afterwards would show a sidebar of empty labels for a
 * frame, and every page below would have to handle `t` being absent — which is
 * a nullable context threaded through forty components to avoid one spinner.
 */
export function I18nProvider({ children }: { children: ReactNode }) {
  const [data, setData] = useState<I18nPayload | null>(null)
  const [error, setError] = useState<Error | null>(null)

  useEffect(() => {
    const controller = new AbortController()

    // The `lang` query parameter is forwarded to the API, because the server
    // resolves a request's language from it before it looks at the cookie
    // (internal/admin/lang.go). Without this the parameter reaches the shell —
    // which is served by the server and does see it — and stops there, so a
    // link pinned to English rendered an English <html lang> around a Chinese
    // page. Exactly the "a link can pin a language" case the parameter exists
    // for: a screenshot, a bug report, a bookmark.
    const pinned = new URLSearchParams(window.location.search).get('lang')
    const query = pinned ? `?lang=${encodeURIComponent(pinned)}` : ''

    request<I18nPayload>(`/i18n${query}`, { signal: controller.signal })
      .then(setData)
      .catch((err: unknown) => {
        // An abort is this component unmounting, not a failure to report.
        if (err instanceof DOMException && err.name === 'AbortError') return

        // No 401 branch, and its absence is the point.
        //
        // This used to be the first place an expired session was noticed — the
        // copy table was behind the session check and is the outermost thing
        // the interface loads, so it failed before anything else and rendered
        // "check the server is running" for the one situation where the server
        // was fine. The endpoint is public now, because the sign-in screen
        // needs these words before it has a session. The session provider is
        // the only thing that decides whether anybody is signed in.
        setError(err instanceof Error ? err : new Error(String(err)))
      })

    return () => controller.abort()
  }, [])

  if (error) {
    // Rendered rather than thrown: the copy table is the one thing that cannot
    // be reported through the interface it is missing from, so this is the
    // interface's only hardcoded sentence. It is in both languages for the same
    // reason the language switcher labels each entry in its own language.
    return (
      <div style={{ padding: 48, font: '14px/1.7 system-ui', maxWidth: 640 }}>
        <h1 style={{ fontSize: 18 }}>无法加载界面文案 / Could not load the interface copy</h1>
        <p className="muted">{error.message}</p>
        <p className="muted">
          GET /admin/api/i18n 失败。请检查服务是否在运行，然后刷新。
          <br />
          The request to GET /admin/api/i18n failed. Check the server and reload.
        </p>
      </div>
    )
  }

  if (!data) {
    // Nothing visible: this resolves within the same page load, and a spinner
    // that appears and disappears in 30ms is a flicker, not feedback.
    return null
  }

  return <Context value={{ t: data.t, lang: data.lang, langs: data.langs }}>{children}</Context>
}

/** The copy table. Throws outside the provider, which is a wiring mistake. */
export function useT(): Messages {
  return useI18n().t
}

/** The table, the language and the language list. */
export function useI18n(): I18n {
  const value = use(Context)
  if (!value) {
    throw new Error('useI18n outside I18nProvider')
  }
  return value
}

/**
 * The URL that switches language.
 *
 * A full navigation to the server's switcher rather than a client-side state
 * change, and that is deliberate. The choice is a cookie the server itself
 * reads — it is what fills in the `lang` attribute on the shell before the
 * first paint, and what the API resolves every request's language from — and
 * the switcher endpoint already knows how to set it and how to return the
 * reader to where they were. Doing it client-side would mean a second
 * implementation of the same rule, and the two would disagree the first time
 * somebody signed out in one language.
 *
 * The cost is a page reload on a rare action.
 */
export function switchLanguageHref(tag: string): string {
  const to = window.location.pathname + window.location.search
  return `/admin/lang/${encodeURIComponent(tag)}?to=${encodeURIComponent(to)}`
}
