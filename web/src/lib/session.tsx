/**
 * Who is signed in, and what to do when nobody is.
 *
 * ---------------------------------------------------------------------------
 * The shell is served without a session check — it is an empty div and a script
 * tag, and putting a redirect in front of it would mean a cookie that expired
 * produces a bounce before the interface has even loaded. So the interface asks
 * this instead, and the answer drives the whole router:
 *
 *   /api/session succeeds    signed in — the shell renders
 *   /api/session says 401    nobody is signed in. Then ask /api/setup: a
 *                            deployment with no administrator yet gets the
 *                            first-run form, and one that has an administrator
 *                            gets the sign-in form.
 *
 * `loading` is a state and not a spinner. It is the state the first paint is
 * in, and the router holds still for it rather than redirecting — a redirect
 * that fires before the answer arrives sends a signed-in operator to the
 * sign-in form on every refresh.
 *
 * ---------------------------------------------------------------------------
 * What this replaced.
 *
 * Every 401 used to be a full page load: `window.location.replace('/admin/login')`,
 * guarded by a module-level flag so that twenty failing requests in one tick
 * produced one navigation rather than twenty history entries. That was correct
 * while the sign-in screen was a server-rendered page outside this router.
 *
 * It is a route now, so forgetting the session is a state change and the router
 * does the rest. The flag is gone with the reload: setting the same state twice
 * is already a no-op in React, which is a better guard than a boolean because
 * it cannot be left set by mistake.
 * ---------------------------------------------------------------------------
 */

import { createContext, use, useCallback, useEffect, useState, type ReactNode } from 'react'

import { UnauthenticatedError, request, type SetupStatus } from './api'
import type { Session as SessionInfo } from './types'

/**
 * The four things the interface can be doing about a session.
 *
 * `error` is separate from `anonymous` on purpose: a 500 or a dead network is
 * not "signed out", and routing to the sign-in form would be a lie about what
 * happened — one that sends the operator to retype a password that was never
 * the problem.
 */
export type AuthState =
  | { status: 'loading' }
  | {
      status: 'anonymous'
      setupRequired: boolean
      /**
       * The server's floor for a new password, for the first-run form.
       *
       * Carried here rather than written into the form because it is the
       * server's rule and the form is only borrowing it. Zero when the answer
       * could not be fetched, which happens only on the path that shows the
       * sign-in form instead — the server refuses a short password either way.
       */
      minPasswordLength: number
    }
  | { status: 'signed-in'; session: SessionInfo }
  | { status: 'error'; error: Error }

export interface Auth {
  state: AuthState
  /**
   * Takes the session the server has just issued.
   *
   * Both signing in and the first-run form end here: the server answers both
   * with a session, so neither has to ask again to find out who they are.
   */
  adopt: (session: SessionInfo) => void
  /** Ends the session on the server, then forgets it. */
  signOut: () => Promise<void>
}

const Context = createContext<Auth | null>(null)

/**
 * The registered way to forget a session, for code that is not a component.
 *
 * ---------------------------------------------------------------------------
 * Module-level rather than a context read, for the same reason the old redirect
 * was: the four places that can discover a session has gone — this provider,
 * the copy table, the checklist and every page's loader — do not share a parent
 * below the provider. Threading a callback to all of them would mean four props
 * through components that have nothing to do with sessions.
 *
 * The provider registers the real implementation as it mounts. A call before
 * that is a no-op and does not need to be anything else: the provider's own
 * /api/session request is in flight at the time, and it reaches the same
 * conclusion a moment later.
 * ---------------------------------------------------------------------------
 */
let forgetSession: (() => void) | null = null

/** Records that the server no longer recognises this session. */
export function forget(): void {
  forgetSession?.()
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: 'loading' })

  const forgetNow = useCallback(() => {
    setState({ status: 'anonymous', setupRequired: false, minPasswordLength: 0 })
  }, [])

  useEffect(() => {
    forgetSession = forgetNow
    return () => {
      if (forgetSession === forgetNow) forgetSession = null
    }
  }, [forgetNow])

  useEffect(() => {
    const controller = new AbortController()

    async function resolve() {
      try {
        const info = await request<SessionInfo>('/session', { signal: controller.signal })
        setState({ status: 'signed-in', session: info })
        return
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return

        if (!(err instanceof UnauthenticatedError)) {
          // A 500, a network failure, a proxy's HTML error page. Not "signed
          // out" — see the note on AuthState.
          setState({ status: 'error', error: err instanceof Error ? err : new Error(String(err)) })
          return
        }
      }

      // Nobody is signed in. Which form they get is the server's answer, and
      // asking is one request rather than a guess: a deployment that has no
      // administrator has nothing to sign in to, and offering a sign-in form
      // for an account that does not exist is a dead end that looks like a bug.
      try {
        const status = await request<SetupStatus>('/setup', { signal: controller.signal })
        setState({
          status: 'anonymous',
          setupRequired: status.required,
          minPasswordLength: status.min_password_length,
        })
      } catch (err) {
        if (err instanceof DOMException && err.name === 'AbortError') return
        // The deployment is claimed as far as this interface knows. Offering
        // the sign-in form is the safe direction: first-run setup is closed on
        // the server anyway, so a wrong guess here is a form that answers 409
        // rather than an account anybody can take.
        setState({ status: 'anonymous', setupRequired: false, minPasswordLength: 0 })
      }
    }

    void resolve()
    return () => controller.abort()
  }, [])

  const adopt = useCallback((session: SessionInfo) => {
    setState({ status: 'signed-in', session })
  }, [])

  const signOut = useCallback(async () => {
    try {
      await request<void>('/logout', { method: 'POST' })
    } finally {
      // Even if the call failed the local session is over: the cookie may
      // already be gone, and leaving the operator on a page whose buttons all
      // return 401 is worse than signing them out twice.
      setState({ status: 'anonymous', setupRequired: false, minPasswordLength: 0 })
    }
  }, [])

  return <Context value={{ state, adopt, signOut }}>{children}</Context>
}

/** The session state and the two ways to change it. */
export function useAuth(): Auth {
  const value = use(Context)
  if (!value) {
    throw new Error('useAuth outside SessionProvider')
  }
  return value
}

/**
 * The signed-in operator.
 *
 * For components that only exist inside the shell. Throws rather than returning
 * null, because reaching it signed out is a wiring mistake — the router's guard
 * is what keeps those components from rendering, and a null check in each of
 * them would be the guard written forty times.
 */
export function useSession(): SessionInfo & { signOut: () => Promise<void> } {
  const { state, signOut } = useAuth()
  if (state.status !== 'signed-in') {
    throw new Error('useSession outside a signed-in route')
  }
  return { ...state.session, signOut }
}
