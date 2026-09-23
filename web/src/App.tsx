import { Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'

import { Empty, LoadFailed, Page, PageHead } from './components/Page'
import { Shell } from './components/Shell'
import { useT } from './lib/i18n'
import { OnboardingProvider } from './lib/onboarding'
import { useAuth } from './lib/session'
import { ApiDocs } from './pages/ApiDocs'
import { Audit } from './pages/Audit'
import { ChannelForm } from './pages/ChannelForm'
import { Channels } from './pages/Channels'
import { Deliveries } from './pages/Deliveries'
import { DeliveryDetail } from './pages/DeliveryDetail'
import { Keys } from './pages/Keys'
import { Password } from './pages/Password'
import { Setup } from './pages/Setup'
import { SignIn } from './pages/SignIn'
import { Start } from './pages/Start'

/**
 * The routes.
 *
 * ---------------------------------------------------------------------------
 * Two trees, and the split is the whole shape of this file.
 *
 *   /login and /setup   outside the shell, because every part of the shell
 *                       promises a session. See AuthCard.
 *
 *   everything else     inside RequireSession, which is what turns a visitor
 *                       without one away — and which carries the destination
 *                       with it, so signing in returns them to the page they
 *                       were trying to reach rather than to a default.
 *
 * The paths are the server's own. This interface used to be mounted at
 * /admin/app beside the server-rendered pages; it is the interface now, so
 * /admin/channels is this route and not a redirect to one. The Go side serves
 * the shell at every path under /admin and knows nothing about this list — see
 * registerSPA in internal/admin/spa.go.
 *
 * The index redirect goes to the channel list, which is where /admin has always
 * gone. The first-run checklist is a page the navigation offers, not a page the
 * interface assumes you need — except straight after setup, which is why Setup
 * navigates there itself.
 * ---------------------------------------------------------------------------
 */
export function App() {
  return (
    <Routes>
      <Route path="login" element={<SignIn />} />
      <Route path="setup" element={<Setup />} />

      <Route element={<RequireSession />}>
        <Route element={<Shell />}>
          <Route index element={<Navigate to="/channels" replace />} />
          <Route path="start" element={<Start />} />
          <Route path="channels" element={<Channels />} />
          {/* `new` before `:name`, which reads as the precedence it is. React
              Router ranks a static segment above a dynamic one regardless of
              order, so this is documentation rather than behaviour — but the
              behaviour is the one the order suggests, and a reader should not
              have to know the ranking rule to be sure of it. */}
          <Route path="channels/new" element={<ChannelForm />} />
          <Route path="channels/:name" element={<ChannelForm />} />
          <Route path="deliveries" element={<Deliveries />} />
          <Route path="deliveries/:id" element={<DeliveryDetail />} />
          <Route path="audit" element={<Audit />} />
          <Route path="keys" element={<Keys />} />
          <Route path="api-docs" element={<ApiDocs />} />
          <Route path="password" element={<Password />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}

/**
 * Everything behind a session.
 *
 * ---------------------------------------------------------------------------
 * It holds still while the answer is in flight, and that is not a detail. The
 * shell is served to anybody, so this guard runs on the very first render of
 * every page load — including one where the operator is signed in and has
 * simply pressed refresh. Redirecting on the initial `loading` state would send
 * them to the sign-in form every time.
 *
 * The destination travels in the location state rather than in a query
 * parameter, so a stale bookmark cannot be made to say "sign in and I will take
 * you to /admin/anything".
 *
 * The checklist provider is mounted here rather than above, because it is the
 * one thing the shell reads that needs a session: /api/onboarding is behind the
 * same check as everything else, and asking for it on the sign-in screen would
 * be a 401 on every visit.
 * ---------------------------------------------------------------------------
 */
function RequireSession() {
  const auth = useAuth()
  const location = useLocation()

  if (auth.state.status === 'loading') return null

  if (auth.state.status === 'error') {
    return <SessionUnavailable error={auth.state.error} />
  }

  if (auth.state.status === 'anonymous') {
    // First-run setup before sign-in, because a deployment with no
    // administrator has nothing to sign in to. The server used to make this
    // decision with a redirect from every page handler; it answers a question
    // now and this is where the answer is acted on.
    return (
      <Navigate
        to={auth.state.setupRequired ? '/setup' : '/login'}
        replace
        state={{ from: location }}
      />
    )
  }

  return (
    <OnboardingProvider>
      <Outlet />
    </OnboardingProvider>
  )
}

/**
 * The session could not be established, and it was not a 401.
 *
 * A 500, a dead network, a proxy answering with an HTML error page. Reported as
 * a page of its own rather than by routing to the sign-in form: the operator's
 * password is not the problem, and asking for it again would be a lie that also
 * loses whatever they were doing.
 */
function SessionUnavailable({ error }: { error: Error }) {
  const t = useT()
  return (
    <Page>
      <PageHead title={t.TitleError} />
      <LoadFailed error={error} />
    </Page>
  )
}

/**
 * A path this interface does not have.
 *
 * Not a redirect to the channel list, which is the tempting alternative: a
 * bookmark that has outlived the page it pointed at should say so, and bouncing
 * somebody to a page they did not ask for hides the fact that the link is
 * stale.
 */
function NotFound() {
  const t = useT()
  return (
    <Page>
      <PageHead title={t.TitleError} />
      <div className="card">
        <Empty title={t.TitleError} body={window.location.pathname} />
      </div>
    </Page>
  )
}
