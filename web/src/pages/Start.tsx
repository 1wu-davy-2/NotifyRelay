import { Link } from 'react-router-dom'

import { Empty, Page, PageHead } from '../components/Page'
import { IconStart } from '../components/icons'
import { useT } from '../lib/i18n'
import { useOnboarding } from '../lib/onboarding'

/**
 * The four steps from a fresh deployment to a notification that has arrived.
 *
 * The step numbers are the order, not the progress. A tick replaces the number
 * when the step is done, so "2" under a tick reads as "you are here" rather
 * than "two done" — which is what the server-rendered version settled on after
 * the alternative, a count, made the page look like a progress bar with no end.
 *
 * Reachable after it is finished, and says so. Somebody who bookmarked it
 * should not get a 404, and four ticks with nothing left to do is not a page.
 */
export function Start() {
  const t = useT()
  const { steps } = useOnboarding()

  // The checklist state is fetched by the shell, so it is here before this page
  // renders. A null here means the request failed, which the shell already
  // treats as "do not offer the checklist" — so this page offers nothing rather
  // than an empty list of four steps.
  if (!steps) return null

  if (steps.done) {
    return (
      <Page>
        <PageHead title={t.TitleStart} description={t.StartIntro} />
        <div className="card">
          <Empty
            icon={<IconStart size={20} />}
            title={t.StartDoneHead}
            body={t.StartDoneBody}
            action={
              <Link className="btn btn-primary" to="/deliveries">
                {t.NavDeliveries}
              </Link>
            }
          />
        </div>
      </Page>
    )
  }

  const items = [
    { done: steps.has_channel, title: t.StartStepChannel, hint: t.StartStepChannelHint, to: '/channels' },
    { done: steps.has_key, title: t.StartStepKey, hint: t.StartStepKeyHint, to: '/keys' },
    { done: steps.has_delivery, title: t.StartStepTest, hint: t.StartStepTestHint, to: '/channels' },
    { done: steps.has_result, title: t.StartStepResult, hint: t.StartStepResultHint, to: '/deliveries' },
  ]

  return (
    <Page>
      <PageHead title={t.TitleStart} description={t.StartIntro} />
      <div className="card">
        {items.map((item, i) => (
          <div key={item.title} className={item.done ? 'step done' : 'step'}>
            <div className="step-num" aria-hidden="true">
              {item.done ? '✓' : i + 1}
            </div>
            <div className="step-body">
              <div className="step-title">{item.title}</div>
              <div className="step-sub">{item.hint}</div>
            </div>
            <Link className="btn btn-ghost btn-sm" to={item.to}>
              {item.done ? t.StartStepDone : t.StartActionGo}
            </Link>
          </div>
        ))}
      </div>
    </Page>
  )
}
