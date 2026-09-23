import { useT } from '../lib/i18n'
import { HTML_FIELDS, type HTMLField } from '../lib/messages'

/**
 * One sentence from the copy table, rendered the way its type says it should
 * be.
 *
 * ---------------------------------------------------------------------------
 * Thirteen of the strings are typed template.HTML on the Go side: the authors
 * wrote <code> and <strong> into them and meant them, and the templates that
 * used to render them interpolated them unescaped. The same strings arrive here
 * as ordinary JSON, and React escapes an ordinary string. Rendering one with
 * {t.APIDocsIntro} produced a paragraph reading "see <code>docs/07-api.md</code>",
 * angle brackets and all, which is how this component came to exist.
 *
 * The set is generated from the Go type rather than written by hand, so it
 * cannot fall out of step with the struct it mirrors — and `field` is typed as
 * the union of those names, so a component that wants markup for a field that
 * has none is a compile error rather than a paragraph with tags in it.
 *
 * dangerouslySetInnerHTML is the right tool here and the name is the warning it
 * is meant to be. What makes it safe is not that this is careful; it is that
 * every string in the table is a sentence this repository's authors wrote. No
 * operator input and no API caller reaches it — the same trust the Go templates
 * already place in these fields.
 * ---------------------------------------------------------------------------
 */
export function Copy({ field }: { field: HTMLField }) {
  const t = useT()
  const value = t[field]

  if (!HTML_FIELDS.has(field)) {
    return <>{value}</>
  }

  return <span dangerouslySetInnerHTML={{ __html: value }} />
}
