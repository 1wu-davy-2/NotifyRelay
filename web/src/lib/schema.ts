/**
 * The generated channel form: reading it, and reading it back.
 *
 * ---------------------------------------------------------------------------
 * What is deliberately NOT here.
 *
 * Which fields a channel type has, what they are called, which are required,
 * which are hidden behind a condition, what the stored value looks like and
 * what the default renders as — all of that is decided by the server and
 * arrives ready-made from GET /api/channel-form. See channelFormData in
 * internal/admin/ui.go for the argument; the short version is that two of those
 * rules cannot be re-derived from the schema a client can see:
 *
 *   requiredWhenShown    A conditional parameter with a default is required
 *                        when it applies and one without a default is not.
 *                        The schema has no field for that; it is a rule about
 *                        the pair.
 *
 *   the default's value  Go marshals `Default any` with omitempty, so a default
 *                        of 0, false or "" is absent from the wire — and
 *                        "absent" is exactly what requiredWhenShown tests for.
 *                        A client deriving it from JSON marks
 *                        httpauth.signature_prefix, wecom.to_user and
 *                        wecom.to_party as required when the server does not.
 *
 * What IS here is the two things that genuinely belong to the browser:
 *
 *   applies()   whether a conditional field is currently on screen. It changes
 *               as the operator types, so no server can answer it.
 *   collect()   turning the form's own controls back into a request body. This
 *               is the only implementation of it now — the server-rendered
 *               form's copy went with the form — so what it has to agree with
 *               is the handler on the other end, which reads absent, empty and
 *               false as three different things.
 * ---------------------------------------------------------------------------
 */

/** One generated form control, as GET /api/channel-form describes it. */
export interface FieldView {
  name: string
  label: string
  description?: string
  /** Which control to draw. */
  kind: 'text' | 'password' | 'number' | 'checkbox' | 'select' | 'list'
  /** The stored value, rendered. Empty for a private field, always. */
  value: string
  checked: boolean
  options?: string[]
  /** Required whenever it is shown — see RequiredNow for the always-shown case. */
  required: boolean
  private: boolean
  /** This private field already holds a value; the input is blank regardless. */
  is_set: boolean
  min?: string
  max?: string
  /** Empty means the field is always shown. */
  show_if_field?: string
  show_if_equals?: string
  /**
   * Required and always shown, so the control itself carries `required`.
   *
   * A conditional field's control does not: its required attribute is set as it
   * appears and cleared as it goes, because a hidden input that is still
   * required is a form that cannot be submitted, and the browser reports it
   * against a box nobody can see.
   */
  required_now: boolean
}

/** One channel type's field set. */
export interface TypeForm {
  type: string
  label: string
  fields: FieldView[]
}

/** What GET /api/channel-form answers with. */
export interface ChannelFormData {
  /** Every registered type, with the edited one's values filled in. */
  forms: TypeForm[]
  /** The name being edited, or `__new__`. */
  editing: string
  /** A link to a channel that is not there any more. */
  missing: boolean
  edit_type?: string
  edit_enabled: boolean
  edit_quota: Record<string, number>
  /** The private parameters that already hold a value. Never the values. */
  edit_secrets?: Record<string, boolean>
}

/** The sentinel the server uses for "creating", not a channel name. */
export const NEW_CHANNEL = '__new__'

/**
 * The form's own state.
 *
 * A checkbox is a boolean and everything else is a string, which is what the
 * controls themselves hold — so reading the form back is a copy rather than a
 * parse, and a half-typed number is never coerced into a number the operator
 * did not finish typing.
 */
export type FieldValues = Record<string, string | boolean>

/** The value a condition compares against, as the server sent it. */
function asComparable(value: string | boolean | undefined): string {
  if (value === undefined) return ''
  // The server renders ShowIfEquals with fmt.Sprint, so a boolean condition
  // arrives as the string "true" — which is what a checked box has to become
  // for the comparison to mean anything.
  return String(value)
}

/**
 * Whether a conditional field is currently on screen.
 *
 * Equality only, and that is the server's design rather than a simplification
 * here: ParamSpec.ShowIf documents why. Every case in this codebase is "this
 * field applies when that enum has this value".
 */
export function applies(field: FieldView, values: FieldValues): boolean {
  if (!field.show_if_field) return true
  return asComparable(values[field.show_if_field]) === field.show_if_equals
}

/** Whether a field is required right now. */
export function requiredNow(field: FieldView, values: FieldValues): boolean {
  if (!field.required) return false
  return applies(field, values)
}

/**
 * Turns the visible type's fields into a request body.
 *
 * ---------------------------------------------------------------------------
 * The omissions are the point, and each one is a decision the server reads
 * differently from the alternative:
 *
 *   A hidden field is not sent. It is behind a condition that is false, so it
 *   does not apply — and sending the value it happens to still hold would
 *   apply a setting the operator has moved away from.
 *
 *   A blank field is not sent, rather than sent empty. Absent means "leave it
 *   alone" and an empty string means "clear it", and the two have to stay
 *   distinguishable or an edit that changes the host would wipe the password.
 *
 *   A blank private field is the same case with more riding on it, since the
 *   browser was never sent the value to begin with. Ticking Clear is the only
 *   way to say "remove it".
 *
 *   A number is sent as a number. This is the one conversion here and it is not
 *   optional: every control in an HTML form holds text, so the rendered default
 *   of an integer parameter is the string "0" — and the server reads an integer
 *   with a type assertion and refuses a string. Posting what the box holds
 *   verbatim is what made every channel type with a numeric parameter refuse to
 *   save with its own defaults untouched, which is the shape of bug that makes
 *   a whole channel type unusable rather than awkward.
 * ---------------------------------------------------------------------------
 */
export function collect(
  fields: FieldView[],
  values: FieldValues,
  clears: Record<string, boolean>,
): Record<string, unknown> {
  const config: Record<string, unknown> = {}

  for (const field of fields) {
    if (!applies(field, values)) continue

    const value = values[field.name]

    if (field.kind === 'checkbox') {
      // An unchecked box is a value, not an absence. A boolean parameter is
      // never required and never "leave it alone" — false is an answer.
      config[field.name] = value === true
      continue
    }

    const text = typeof value === 'string' ? value : ''

    if (field.private && clears[field.name]) {
      config[field.name] = ''
      continue
    }

    if (text === '') continue

    // A list parameter arrives as comma-separated text. The channel readers
    // accept a bare string as a one-element list, so "a, b" would otherwise be
    // a single address containing a comma — accepted, and wrong.
    if (field.kind === 'list') {
      config[field.name] = splitList(text)
      continue
    }

    // A numeric parameter arrives as text like everything else, and has to
    // leave as a number — see the note in this file's header. A box the browser
    // considers invalid reports an empty value rather than the nonsense that
    // was typed, so the finite check is a backstop and not the main guard; a
    // value that fails it is left out rather than sent as NaN, and the server's
    // own required/bounds check is what complains.
    if (field.kind === 'number') {
      const n = Number(text)
      if (Number.isFinite(n)) config[field.name] = n
      continue
    }

    config[field.name] = text
  }

  return config
}

/** Comma-separated text to a list, the way every list field on this page is typed. */
export function splitList(value: string): string[] {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
}

/**
 * The starting state for a type's fields.
 *
 * The values come from the server, already rendered — including the ones that
 * are a default rather than a stored setting, which is why this is a copy and
 * not a lookup into the channel's configuration.
 */
export function initialValues(fields: FieldView[]): FieldValues {
  const out: FieldValues = {}
  for (const field of fields) {
    out[field.name] = field.kind === 'checkbox' ? field.checked : field.value
  }
  return out
}
