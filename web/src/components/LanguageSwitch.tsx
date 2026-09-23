import { switchLanguageHref, useI18n } from '../lib/i18n'

/**
 * The language switcher.
 *
 * Each entry is labelled in its own language and not in the current one: the
 * person this is for is the one who cannot read the page they are on, and
 * labelling the way out in the language they cannot read is labelling it for
 * everybody except them. The labels come from the server for that reason —
 * `中文` and `English` are properties of the languages, not of the table.
 *
 * A real link, not a button with an onClick. It navigates, which is what makes
 * the choice a cookie the server-rendered sign-in page also reads — and it is
 * what lets somebody middle-click it or see where it goes.
 */
export function LanguageSwitch() {
  const { langs, t } = useI18n()

  return (
    <nav className="langs" aria-label={t.NavLangSwitch}>
      {langs.map((l) => (
        <a
          key={l.tag}
          href={switchLanguageHref(l.tag)}
          hrefLang={l.tag}
          className={l.on ? 'on' : undefined}
          aria-current={l.on ? 'true' : undefined}
        >
          {l.label}
        </a>
      ))}
    </nav>
  )
}
