/**
 * The first-run checklist's state.
 *
 * The navigation offers the checklist only while something is left to do — a
 * "get started" link that is still there after the operator has started is a
 * link that teaches them to stop reading the sidebar. That means the shell
 * needs this on every page, which is why the server computes it in one endpoint
 * rather than leaving the client to derive it from three.
 *
 * Exposed as a context with a refresh rather than fetched per page, because the
 * two things that change it — creating a channel and creating a key — happen on
 * pages that then need the sidebar to update without a reload.
 */

import { createContext, use, useCallback, useEffect, useState, type ReactNode } from 'react'

import { UnauthenticatedError, request } from './api'
import { forget } from './session'

export interface Onboarding {
  has_channel: boolean
  has_key: boolean
  has_delivery: boolean
  has_result: boolean
  done: boolean
  remaining: number
}

interface OnboardingContext {
  steps: Onboarding | null
  /** Re-reads the state. Call after an action that finishes a step. */
  refresh: () => void
}

const Context = createContext<OnboardingContext | null>(null)

export function OnboardingProvider({ children }: { children: ReactNode }) {
  const [steps, setSteps] = useState<Onboarding | null>(null)
  const [nonce, setNonce] = useState(0)

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    const controller = new AbortController()

    request<Onboarding>('/onboarding', { signal: controller.signal })
      .then(setSteps)
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') return
        if (err instanceof UnauthenticatedError) {
          forget()
          return
        }
        // A failure here is not worth a page-level error: the checklist is a
        // convenience, and the sidebar simply does not offer it. The pages that
        // matter report their own failures.
        setSteps(null)
      })

    return () => controller.abort()
  }, [nonce])

  return <Context value={{ steps, refresh }}>{children}</Context>
}

/** The checklist's state, or null while it is unknown. */
export function useOnboarding(): OnboardingContext {
  const value = use(Context)
  if (!value) {
    throw new Error('useOnboarding outside OnboardingProvider')
  }
  return value
}
