import { NavLink } from 'react-router-dom'

import { useT } from '../lib/i18n'
import { useOnboarding } from '../lib/onboarding'
import {
  IconAPI,
  IconAudit,
  IconChannels,
  IconDeliveries,
  IconKeys,
  IconLogo,
  IconStart,
} from './icons'

/**
 * The sidebar.
 *
 * The "get started" entry is offered only while something is left to do, which
 * is the rule the server-rendered navigation already follows: a link that is
 * still there after the operator has started is a link that teaches them to
 * stop reading the sidebar. The count beside it is the reason it is worth
 * fetching the checklist state on every page.
 *
 * The order is the server-rendered sidebar's, which is not the mockup's. Kept
 * because it is what an operator's muscle memory is built on, and reordering a
 * navigation is the kind of change that costs somebody a week of misclicks for
 * no benefit they can name.
 */
export function Sidebar() {
  const t = useT()
  const { steps } = useOnboarding()

  const items = [
    { to: '/start', label: t.NavStart, icon: <IconStart />, badge: steps?.remaining },
    { to: '/channels', label: t.NavChannels, icon: <IconChannels /> },
    { to: '/deliveries', label: t.NavDeliveries, icon: <IconDeliveries /> },
    { to: '/audit', label: t.NavAudit, icon: <IconAudit /> },
    { to: '/keys', label: t.NavKeys, icon: <IconKeys /> },
    { to: '/api-docs', label: t.NavAPI, icon: <IconAPI /> },
  ].filter((item) => item.badge === undefined || item.badge > 0)

  return (
    <aside className="side">
      <NavLink to="/channels" className="brand">
        <span className="logo">
          <IconLogo />
        </span>
        <span>{t.AppName}</span>
      </NavLink>

      <nav className="nav">
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            className={({ isActive }) => (isActive ? 'on' : undefined)}
          >
            {item.icon}
            <span>{item.label}</span>
            {item.badge !== undefined && <span className="pill tag">{item.badge}</span>}
          </NavLink>
        ))}
      </nav>

      {/* The product's English name, under the localised one. The mark in the
          corner and the name in the sidebar are both translated; this is what
          somebody searching for the project upstream will recognise. */}
      <div className="side-foot">
        <div className="brand-sub">Courier Hub</div>
      </div>
    </aside>
  )
}
