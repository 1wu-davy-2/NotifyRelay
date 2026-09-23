package sqlite

// schema is the full DDL, applied on every open.
//
// CREATE TABLE IF NOT EXISTS is the migration strategy for now: the service
// has exactly one writer and one schema version, so a migration framework
// would be machinery for a problem that does not exist yet. When a column
// needs to change, that is the moment to introduce one.
//
// Timestamps are Unix nanoseconds in INTEGER columns. Text timestamps would
// invite a comparison against a string in a different format, which sorts
// correctly right up until it does not.
const schema = `
CREATE TABLE IF NOT EXISTS deliveries (
    id              TEXT    PRIMARY KEY,
    request_id      TEXT    NOT NULL,
    -- target is the reference the caller wrote, echoed back to them. channel is
    -- the instance it resolved to at accept time, which is what the queue, the
    -- metrics and "show me everything for oncall" all key on — one column
    -- cannot be both without making the other one unstable.
    target          TEXT    NOT NULL,
    channel         TEXT    NOT NULL DEFAULT '',
    -- recipients is a JSON array of caller-supplied addresses, empty when the
    -- channel's own configuration decided them.
    recipients      TEXT    NOT NULL DEFAULT '',
    channel_type    TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_error      TEXT    NOT NULL DEFAULT '',
    last_class      TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    claimed_at      INTEGER NOT NULL DEFAULT 0,
    sent_at         INTEGER NOT NULL DEFAULT 0
);

-- The claim query's index. Without it every claim scans the whole table,
-- which is the one operation that has to stay fast as the queue grows.
CREATE INDEX IF NOT EXISTS idx_deliveries_due
    ON deliveries (status, next_attempt_at);

-- The orphan sweep's index: it looks for in-flight rows by claim age, which
-- has nothing to do with when they are next due.
CREATE INDEX IF NOT EXISTS idx_deliveries_claimed
    ON deliveries (status, claimed_at);

CREATE INDEX IF NOT EXISTS idx_deliveries_request
    ON deliveries (request_id);

CREATE INDEX IF NOT EXISTS idx_deliveries_created
    ON deliveries (created_at);

CREATE TABLE IF NOT EXISTS attempts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id TEXT    NOT NULL REFERENCES deliveries (id) ON DELETE CASCADE,
    request_id  TEXT    NOT NULL,
    target      TEXT    NOT NULL,
    channel_type TEXT   NOT NULL,
    attempt_no  INTEGER NOT NULL,
    class       TEXT    NOT NULL,
    detail      TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    -- Why the channel was never called (breaker_open | quota_exhausted |
    -- rate_limited). Empty for an attempt that reached the channel. The class
    -- alone cannot say this: a skipped delivery and an unreachable one are
    -- both CONNECT_ERROR.
    skip_reason TEXT    NOT NULL DEFAULT '',
    elapsed_ms  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attempts_delivery
    ON attempts (delivery_id, id);

CREATE INDEX IF NOT EXISTS idx_attempts_created
    ON attempts (created_at);

CREATE TABLE IF NOT EXISTS idempotency (
    key        TEXT    PRIMARY KEY,
    request_id TEXT    NOT NULL,
    status     INTEGER NOT NULL,
    body       BLOB    NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_idempotency_created
    ON idempotency (created_at);

CREATE TABLE IF NOT EXISTS breakers (
    channel    TEXT    PRIMARY KEY,
    state      TEXT    NOT NULL,
    failures   INTEGER NOT NULL DEFAULT 0,
    opened_at  INTEGER NOT NULL DEFAULT 0,
    probes     INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

-- Day and month allowances live here rather than in memory: they are the
-- windows a platform actually enforces, and a restart that forgot them would
-- hand back an allowance that has already been spent.
--
-- The shorter windows are kept in memory. Losing a few seconds of counts costs
-- nothing, and a row per second per channel would be all cost.
CREATE TABLE IF NOT EXISTS quota_counters (
    channel    TEXT    NOT NULL,
    period     TEXT    NOT NULL,
    count      INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (channel, period)
);

CREATE INDEX IF NOT EXISTS idx_quota_counters_updated
    ON quota_counters (updated_at);

-- Small key/value facts about this database that are not configuration.
--
-- Today it holds one: whether the configuration file's channels block has
-- already been imported. The obvious alternative — treating an empty
-- channel_instances table as "never imported" — is wrong in the one case that
-- matters: an operator who deletes every channel on purpose gets them all back
-- at the next restart, forever, and stops trusting the UI.
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- What operators did through the admin surface.
--
-- Separate from the delivery audit because it answers a different question:
-- not "what happened to this message" but "who turned off the thing that was
-- protecting it". Kept forever by default; the retention sweep leaves it alone.
CREATE TABLE IF NOT EXISTS admin_audit (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    at     INTEGER NOT NULL,
    actor  TEXT    NOT NULL,
    action TEXT    NOT NULL,
    target TEXT    NOT NULL DEFAULT '',
    detail TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_at ON admin_audit (at);

-- Channel configuration, so it can be edited without restarting the service.
--
-- config_json holds the channel's own parameter block. Values the channel
-- declares private are sealed by the writer before they get here, so this
-- column contains ciphertext for exactly those fields and readable text for the
-- rest — which is what makes a database dump safe to attach to a ticket, and
-- still useful once it is there.
CREATE TABLE IF NOT EXISTS channel_instances (
    name        TEXT    PRIMARY KEY,
    type        TEXT    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    config_json TEXT    NOT NULL DEFAULT '{}',
    quota_json  TEXT    NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Producer credentials.
--
-- key_hash is the sha256 digest, never the token. The unique index is what
-- makes the lookup on the authentication path an index seek rather than a scan
-- of every key for every notification, and it is also what makes a duplicate
-- digest impossible to store twice.
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    key_hash     TEXT    NOT NULL UNIQUE,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    -- JSON array of address patterns this key may name as a recipient. Empty
    -- means none, which is the safe reading: a key created before this column
    -- existed must not silently gain the right to address anyone.
    allowed_recipients TEXT NOT NULL DEFAULT ''
);

-- The operator's sign-in.
--
-- One row, enforced rather than assumed: the CHECK on the primary key means a
-- second credential cannot be created by a bug, and the absence of the row is
-- an unambiguous "nobody has claimed this deployment yet" — which is what opens
-- the first-run page.
CREATE TABLE IF NOT EXISTS admin_credentials (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    username      TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);
`
