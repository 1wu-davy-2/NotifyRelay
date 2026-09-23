/**
 * The client for the admin's own JSON API.
 *
 * Everything the interface does goes through here, for two reasons that are
 * both about the server's rules rather than about tidiness:
 *
 *   CSRF. The session middleware rejects any state-changing request without the
 *   `X-NotifyRelay-Admin` header (internal/admin/admin.go). A raw fetch from a
 *   component would be a 403 that looks like a permissions problem. Sending it
 *   in one place means it cannot be forgotten in one place.
 *
 *   Session expiry. A cookie that has timed out answers 401 with a JSON body,
 *   and a component that rendered that body would show "unauthenticated" in a
 *   table cell. Turning it into a typed error here lets the shell catch it once
 *   and send the operator to the sign-in page.
 *
 * The server decodes request bodies with `DisallowUnknownFields`, so a body
 * carrying a field the Go struct does not declare is a 400 rather than an
 * ignored extra. That is why every call below names its own body type instead
 * of spreading an object through.
 */

import type { ChannelFormData } from './schema'
import type {
  APIKey,
  AuditEntry,
  Channel,
  CreatedKey,
  Delivery,
  DeliveryDetail,
  Session,
  Stats,
} from './types'

/** Where the admin mounts its API. Set by internal/admin/admin.go. */
const BASE = '/admin/api'

/**
 * The CSRF header the session middleware requires.
 *
 * The value is not checked — the header's presence is the whole test, because a
 * cross-origin form post cannot set a custom header without a preflight the
 * server would have to answer. Kept as a named constant so the name appears
 * once on this side too.
 */
const CSRF_HEADER = 'X-NotifyRelay-Admin'

/** An error the server described. `code` is its machine-readable tag. */
export class APIError extends Error {
  readonly status: number
  readonly code: string
  /**
   * Per-parameter complaints, keyed by the schema name of the parameter.
   *
   * A channel's own validation refuses a configuration and says which
   * parameters it objected to. The message is one sentence naming every
   * parameter, which is written to be read in a log line — the form uses this
   * map to put each complaint under the box it is about instead, and the
   * message for everything it could not place. See writeValidationError.
   */
  readonly fields?: Record<string, string>

  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
    this.fields = fields
  }
}

/**
 * The session is gone.
 *
 * A separate class from APIError because the response is not "this action
 * failed" but "everything on this page is stale" — the shell catches it and
 * navigates, and no component should be rendering it inline.
 */
export class UnauthenticatedError extends APIError {
  constructor(message: string) {
    super(401, 'unauthenticated', message)
    this.name = 'UnauthenticatedError'
  }
}

/** The shape of the server's error body (internal/admin.admin.go). */
interface ErrorBody {
  error?: string
  message?: string
  /** Present only when a channel refused a configuration it was given. */
  fields?: Record<string, string>
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'DELETE'
  body?: unknown
  /** Abort a request whose component unmounted. */
  signal?: AbortSignal
}

/**
 * Performs one request and returns the decoded body.
 *
 * A 204 or an empty body decodes to `undefined` rather than throwing: several
 * endpoints answer with nothing on success, and a JSON parse error there would
 * read as a failure.
 *
 * Exported for the few callers that are not a named resource — the copy table,
 * mainly — so that they go through the same CSRF, cookie and error handling
 * rather than reaching for fetch and quietly opting out of all three.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'

  const headers: Record<string, string> = {}
  if (method !== 'GET') headers[CSRF_HEADER] = '1'
  if (options.body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(BASE + path, {
    method,
    headers,
    // The session is a cookie. Same-origin is the default, stated because the
    // day this is served from another host it must be 'include' and the silent
    // failure otherwise is an interface that loads and then says everything is
    // unauthenticated.
    credentials: 'same-origin',
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  })

  if (!res.ok) {
    // The server answers every failure with {error, message}. A body that is
    // not that — a proxy's HTML error page, a truncated response — still has to
    // become an error rather than an exception about JSON.
    let code = 'http_' + res.status
    let message = res.statusText || `HTTP ${res.status}`
    let fields: Record<string, string> | undefined
    try {
      const parsed = (await res.json()) as ErrorBody
      if (parsed.error) code = parsed.error
      if (parsed.message) message = parsed.message
      fields = parsed.fields
    } catch {
      // Keep the status line. Nothing better is on offer.
    }

    if (res.status === 401) throw new UnauthenticatedError(message)
    throw new APIError(res.status, code, message, fields)
  }

  if (res.status === 204) return undefined as T

  const text = await res.text()
  if (text.trim() === '') return undefined as T
  return JSON.parse(text) as T
}

// ------------------------------------------------------------------ session

export const session = {
  /**
   * Who is signed in, or a 401.
   *
   * The 401 is the interesting answer and it is why this is called rather than
   * assumed: the shell is served to anybody, so the interface's first question
   * is whether there is a session at all.
   */
  get: (signal?: AbortSignal) => request<Session>('/session', { signal }),

  /** Answers with the new session, so the caller does not have to ask again. */
  login: (username: string, password: string) =>
    request<Session>('/login', { method: 'POST', body: { username, password } }),

  logout: () => request<void>('/logout', { method: 'POST' }),

  changePassword: (current_password: string, new_password: string) =>
    request<PasswordChange>('/password', {
      method: 'POST',
      body: { current_password, new_password },
    }),
}

/** What a password change answers. */
export interface PasswordChange {
  /** How many *other* sessions the change ended. This one is kept. */
  sessions_ended: number
  /** The server's own sentence about it, already in the reader's language. */
  message: string
}

/** What GET /admin/api/setup answers. */
export interface SetupStatus {
  /** This deployment has no administrator yet, so the first-run form applies. */
  required: boolean
  /**
   * The floor the server enforces on a new password.
   *
   * Sent rather than written down here. Three copies of this number used to
   * exist — the server's rule, the server's message, and the form's minlength —
   * and a browser that accepts a password the server then refuses reads as a
   * bug in whichever of the two the operator happened to believe.
   */
  min_password_length: number
}

// -------------------------------------------------------------------- setup

/**
 * First run.
 *
 * Both of these are reachable without a session, and both have to be: `status`
 * is what the sign-in screen asks to decide which form to draw, and `create`
 * makes the account every later request is checked against. The server closes
 * the pair the moment an administrator exists.
 */
export const setup = {
  status: (signal?: AbortSignal) => request<SetupStatus>('/setup', { signal }),

  /** Answers with a session: the operator is signed in without a second step. */
  create: (username: string, password: string) =>
    request<Session>('/setup', { method: 'POST', body: { username, password } }),
}

// ----------------------------------------------------------------- channels

export const channels = {
  list: (signal?: AbortSignal) =>
    request<{ channels: Channel[] }>('/channels', { signal }).then((r) => r.channels ?? []),

  get: (name: string, signal?: AbortSignal) =>
    request<Channel>(`/channels/${encodeURIComponent(name)}`, { signal }),

  /**
   * The generated form, for a create or an edit.
   *
   * `name` absent means a create. The fields come back already built — labelled,
   * typed, ordered, with the stored values rendered and the private ones
   * withheld — rather than as a schema the client would have to turn into a
   * form itself. See web/src/lib/schema.ts.
   */
  form: (name?: string, signal?: AbortSignal) =>
    request<ChannelFormData>(`/channel-form${name ? `?name=${encodeURIComponent(name)}` : ''}`, {
      signal,
    }),

  /**
   * Creates or updates, depending on `editing`.
   *
   * One endpoint for both, because a channel's name is its identity: a create
   * that collides with an existing name is an edit, and the server decides
   * which by comparing `editing` against the name it was given.
   */
  save: (body: ChannelSaveBody) => request<Channel>('/channels', { method: 'POST', body }),

  remove: (name: string) =>
    request<void>(`/channels/${encodeURIComponent(name)}`, { method: 'DELETE' }),

  /**
   * Reaches the endpoint with the stored configuration.
   *
   * Answers 200 whether or not the probe succeeded — the class says which, and
   * a failure is a result rather than an error. `detail` is the channel's own
   * account of what happened, already translated.
   */
  test: (name: string) =>
    request<ChannelTestResult>(`/channels/${encodeURIComponent(name)}/test`, { method: 'POST' }),

  /**
   * Sends a real message through the real path.
   *
   * Distinct from `test`, which only proves the endpoint is reachable. This one
   * exercises the router, the queue and the channel, and its result appears in
   * the delivery list — which is the point of it.
   *
   * Answers 202, not 200: the delivery has been queued, not sent. The reply
   * carries the id to watch, and the page it leads to is where "did it work" is
   * actually answered — an interface that reported success here would be
   * claiming a delivery it has not seen yet.
   */
  testNotification: (name: string, title: string, body: string) =>
    request<TestNotificationResult>(
      `/channels/${encodeURIComponent(name)}/test-notification`,
      { method: 'POST', body: { title, body } },
    ),

  resetBreaker: (name: string) =>
    request<void>(`/channels/${encodeURIComponent(name)}/breaker/reset`, { method: 'POST' }),
}

/**
 * What a connectivity probe answers.
 *
 * `class` is one of the three delivery outcomes the router uses —
 * CONNECT_ERROR, TRANSIENT, PERMANENT — plus SENT. `ok` is a convenience the
 * server computes, kept rather than derived here so that "which classes count
 * as success" stays one rule.
 */
export interface ChannelTestResult {
  channel: string
  class: string
  ok: boolean
  detail?: string
  error?: string
}

/**
 * What a test notification answers with.
 *
 * `delivery_id` is absent when the queue accepted the message but produced no
 * delivery to point at — the page falls back to the delivery list rather than
 * inventing an id.
 */
export interface TestNotificationResult {
  request_id: string
  delivery_id?: string
}

/** The body of POST /admin/api/channels. Mirrors internal/admin.saveRequest. */
export interface ChannelSaveBody {
  name: string
  type: string
  enabled: boolean
  config: Record<string, unknown>
  quota: Record<string, number>
  /**
   * What the caller believed it was doing: the channel's current name when
   * editing, `__new__` when creating. The server compares it against `name` to
   * tell a rename from a collision.
   */
  editing: string
  /**
   * The caller has seen the name collision and means it anyway.
   *
   * A collision on a create is a question rather than a failure — saving would
   * replace a channel that may be delivering right now — so the server refuses
   * with `name_taken` and the form asks before sending this.
   */
  replace?: boolean
}

// --------------------------------------------------------------- deliveries

/**
 * The delivery list's filters, matching what the handler reads.
 *
 * `target` rather than `channel`, which is the name the store filters on and
 * the name that ends up in the query string. Renaming it here to match the
 * interface's vocabulary would mean a translation layer that has to be kept
 * correct in both directions, for one word.
 */
export interface DeliveryQuery {
  status?: string
  target?: string
  request_id?: string
  limit?: number
  offset?: number
}

export const deliveries = {
  list: (query: DeliveryQuery = {}, signal?: AbortSignal) => {
    const params = new URLSearchParams()
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== '' && value !== null) params.set(key, String(value))
    }
    const qs = params.toString()
    return request<{ deliveries: Delivery[] }>(`/deliveries${qs ? '?' + qs : ''}`, {
      signal,
    }).then((r) => r.deliveries ?? [])
  },

  get: (id: string, signal?: AbortSignal) =>
    request<DeliveryDetail>(`/deliveries/${encodeURIComponent(id)}`, { signal }),

  replay: (id: string) =>
    request<void>(`/deliveries/${encodeURIComponent(id)}/replay`, { method: 'POST' }),

  stats: (signal?: AbortSignal) => request<Stats>('/stats', { signal }),
}

// ----------------------------------------------------------------- api docs

/** One worked example, in one language. */
export interface APIDocSample {
  id: string
  label: string
  note?: string
  /** The code, with the base URL already substituted by the server. */
  body: string
}

/** One row of the endpoint table. */
export interface APIDocEndpoint {
  method: string
  path: string
  /** Empty for an endpoint that takes no credential. */
  auth?: string
  purpose: string
}

/** One row of the error-code table. */
export interface APIDocError {
  code: string
  status: string
  meaning: string
}

export interface APIDocs {
  base_url: string
  /**
   * Whether the operator's own connection to this page is TLS.
   *
   * False means the token they are about to copy into a script will cross the
   * network in the clear, which the page says out loud.
   */
  secure: boolean
  samples: APIDocSample[]
  endpoints: APIDocEndpoint[]
  errors: APIDocError[]
}

/**
 * The reference.
 *
 * Served rather than written here: the fourteen endpoints, nine error codes and
 * seven samples are assembled by the server from the same tables and sample
 * files it ships. A second copy in TypeScript would be a second place to add an
 * endpoint, and the place that gets forgotten is the one a caller reads.
 */
export const apiDocs = {
  get: (signal?: AbortSignal) => request<APIDocs>('/api-docs', { signal }),
}

// -------------------------------------------------------------------- audit

export const audit = {
  /**
   * The audit trail, and whether this deployment keeps one.
   *
   * `enabled` is carried through rather than flattened to a list, because an
   * empty list means two different things: nothing has happened yet, or there
   * is no trail configured at all. The page says which.
   */
  list: (signal?: AbortSignal) =>
    request<{ actions: AuditEntry[] | null; enabled: boolean }>('/audit', { signal }).then((r) => ({
      actions: r.actions ?? [],
      enabled: r.enabled,
    })),
}

// --------------------------------------------------------------------- keys

/**
 * The body of POST /admin/api/keys and POST /admin/api/keys/{id}.
 *
 * Both fields are optional and an absent one is left alone — the toggle and the
 * allow list are edited in different places, and requiring both on every call
 * would make flipping a key off and on again silently rewrite a list the caller
 * never saw. That is why these are `?` and not `| undefined` with a default.
 */
export interface KeySaveBody {
  name?: string
  enabled?: boolean
  allowed_recipients?: string[]
}

export const keys = {
  list: (signal?: AbortSignal) =>
    request<{ keys: APIKey[] }>('/keys', { signal }).then((r) => r.keys ?? []),

  /** The reply carries the plaintext key. It is not retrievable afterwards. */
  create: (body: KeySaveBody) => request<CreatedKey>('/keys', { method: 'POST', body }),

  /**
   * Enables, disables, or replaces the allow list.
   *
   * The reply is the new state and not the whole key: `{id, enabled}` is what
   * the server sends, and a caller that wants the rest reloads the list.
   */
  update: (id: string, body: KeySaveBody) =>
    request<{ id: string; enabled: boolean }>(`/keys/${encodeURIComponent(id)}`, {
      method: 'POST',
      body,
    }),

  remove: (id: string) =>
    request<void>(`/keys/${encodeURIComponent(id)}`, { method: 'DELETE' }),
}
