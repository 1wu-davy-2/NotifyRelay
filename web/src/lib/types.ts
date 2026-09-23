/**
 * The shapes the admin API answers with.
 *
 * Hand-written mirrors of the Go structs in internal/admin, which is a
 * duplication and a deliberate one. The alternative — generating them — needs a
 * generator in the build, and the thing being generated is a few hundred lines
 * that change when a handler changes. The cost of the duplication is that a
 * field renamed in Go and not here is a TypeScript error at the use site, which
 * is the failure mode worth having: it is a build error, not a blank cell.
 *
 * Note for anyone adding a request type: the server decodes with
 * `DisallowUnknownFields`, so a field sent that Go does not declare is a 400
 * rather than an ignored extra. `npm run typecheck` cannot catch that — it is
 * the reason these are kept beside the handlers that read them.
 */

/** One channel instance. Mirrors internal/admin.channelView. */
export interface Channel {
  name: string
  type: string
  enabled: boolean
  config: Record<string, unknown>
  quota: Quota
  /** The private parameters that currently hold a value. Never their values. */
  secrets_set?: string[]
  /** Loaded in the router right now, which is not the same as existing. */
  live: boolean
  updated_at?: string
  /** No destination of its own, so a test notification has nowhere to go. */
  needs_recipients?: boolean
}

export interface Quota {
  per_second: number
  per_minute: number
  per_hour: number
  per_day: number
  per_month: number
}

/** The five quota fields, in the order the form shows them. */
export const QUOTA_FIELDS: ReadonlyArray<keyof Quota> = [
  'per_second',
  'per_minute',
  'per_hour',
  'per_day',
  'per_month',
]

/**
 * One delivery attempt. Mirrors internal/admin.attemptView.
 *
 * `class` is one of the router's outcomes, `skip_reason` is set only when the
 * attempt never reached the endpoint, and both are shown as they arrive: they
 * are identifiers the API reference documents, not sentences to translate.
 */
export interface Attempt {
  attempt_no: number
  class: string
  skip_reason?: string
  detail?: string
  error?: string
  elapsed_ms: number
  created_at: string
  channel_type: string
  target: string
}

/** One delivery. Mirrors internal/admin.deliveryView. */
export interface Delivery {
  id: string
  request_id: string
  target: string
  channel_type: string
  status: string
  attempts: number
  last_error?: string
  last_class?: string
  created_at: string
  updated_at: string
  next_attempt_at?: string
  sent_at?: string
  /**
   * Computed by the server from the status and whether the message body is
   * still on disk. A button offered on the client's own reading of `status`
   * would offer a replay of a body that is gone.
   */
  replayable: boolean
}

/**
 * A delivery with its attempt history, as GET /api/deliveries/{id} answers.
 *
 * Wrapped rather than flattened onto the delivery: the two are different
 * lifetimes — the delivery row is one record, the attempts are a log that grows
 * — and a client that had to guess which fields were which would get it wrong
 * the first time a field name collided.
 */
export interface DeliveryDetail {
  delivery: Delivery
  attempts: Attempt[]
}

/** Queue counters. Mirrors store.Stats. */
export interface Stats {
  queued?: number
  sending?: number
  sent?: number
  failed?: number
}

/** One API key. The secret is never in this shape — only in the create reply. */
export interface APIKey {
  id: string
  name: string
  enabled: boolean
  created_at: string
  last_used_at?: string
  /** `database` or `config`: which entries this page may delete. */
  source: string
  /** Address patterns this key may name as a recipient. Empty means none. */
  allowed_recipients?: string[]
}

/** One audit entry. */
export interface AuditEntry {
  at: string
  actor: string
  action: string
  target?: string
  detail?: string
}

/** The signed-in operator. Mirrors internal/admin.sessionResponse. */
export interface Session {
  actor: string
  expires_in_seconds: number
}

/**
 * What POST /admin/api/keys answers with, once, at creation.
 *
 * `token` is the plaintext key and this is the only response that carries it —
 * the store keeps a digest, so no other endpoint can return it and the page
 * cannot show it again. `warning` is the server's own sentence saying so, kept
 * rather than replaced with local copy so the two cannot disagree.
 */
export interface CreatedKey {
  id: string
  name: string
  token: string
  warning?: string
}

