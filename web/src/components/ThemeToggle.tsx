import { useState } from 'react'

import { useT } from '../lib/i18n'
import { apply, current, other, type Theme } from '../lib/theme'
import { IconMoon, IconSun } from './icons'

/**
 * The light/dark switch.
 *
 * The theme lives on <html> rather than in React state, because it has to be
 * there before React renders — see src/lib/theme.ts. So this component holds a
 * copy for rendering and writes through to the document on every change, which
 * is the one place the two can disagree and the reason they are written
 * together.
 *
 * Rendered as a button rather than as a checkbox in a label. The knob is drawn
 * by CSS from the `data-theme` attribute, so it is already correct before this
 * component mounts; a form control would be a second source of truth for the
 * same state.
 */
export function ThemeToggle() {
  const t = useT()
  const [theme, setTheme] = useState<Theme>(current)

  function toggle() {
    const next = other()
    apply(next)
    setTheme(next)
  }

  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggle}
      aria-label={t.NavThemeSwitch}
      title={t.NavThemeSwitch}
    >
      {theme === 'dark' ? <IconMoon /> : <IconSun />}
      <span>{theme === 'dark' ? t.ThemeDark : t.ThemeLight}</span>
      <span className="switch" />
    </button>
  )
}
