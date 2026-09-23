/**
 * Loading one thing, with the three states every page has.
 *
 * Small enough to write inline in each page and worth not doing that: the
 * abort-on-unmount is the part that gets forgotten, and a page that sets state
 * after navigating away is a warning in the console and, occasionally, a wrong
 * number in a table.
 *
 * Deliberately not a data-fetching library. Every page here loads one resource,
 * shows it, and reloads it after a mutation — there is no cache to invalidate,
 * no request to deduplicate, and no polling. A dependency that solves those
 * would be a dependency to keep updated for the one page that eventually needs
 * it.
 */

import { useCallback, useEffect, useState } from 'react'

import { UnauthenticatedError } from './api'
import { forget } from './session'

export interface Async<T> {
  data: T | null
  error: Error | null
  loading: boolean
  /** Re-runs the loader. Call after a mutation. */
  reload: () => void
}

/**
 * Runs `load` on mount and whenever it changes.
 *
 * Pass a loader wrapped in useCallback, or the effect re-runs on every render.
 * The signal is provided so a slow request can be abandoned rather than
 * resolving into a component that has already gone.
 */
export function useAsync<T>(
  load: (signal: AbortSignal) => Promise<T>,
  deps: unknown[] = [],
): Async<T> {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)

    load(controller.signal)
      .then((value) => {
        setData(value)
        setError(null)
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') return
        if (err instanceof UnauthenticatedError) {
          // The session went away between the shell rendering and this request.
          // Forgetting it hands the decision to the router, which shows the
          // sign-in screen and keeps the operator's place in the history.
          forget()
          return
        }
        setError(err instanceof Error ? err : new Error(String(err)))
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })

    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce, ...deps])

  return { data, error, loading, reload }
}
