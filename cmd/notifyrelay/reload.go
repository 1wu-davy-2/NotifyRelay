package main

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"notifyrelay/internal/api"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/config"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/router"
)

// reloader applies a new configuration to a running service.
//
// It exists because "restart the service" is not an acceptable answer during an
// incident, and an incident is exactly when somebody wants to raise a timeout,
// slow the retries down or rotate a key that has leaked. Every change here is
// an atomic swap of a value that is read per use, so nothing in flight is
// interrupted: a delivery being retried keeps its attempt count and picks the
// new cadence up on its next decision.
//
// What it deliberately does not touch is everything that owns a listener, a
// connection or a goroutine. Those cannot be replaced underneath themselves,
// and pretending otherwise would produce a service that reports a new address
// while still serving the old one. They are named in the warning instead, so
// the operator learns which half of their edit took effect.
type reloader struct {
	path     string
	level    *slog.LevelVar
	live     *api.Live
	router   *router.Router
	worker   *queue.Worker
	breakers *breaker.Manager
	channels *config.ChannelSource
	log      *slog.Logger

	// current is the configuration the running components were last built
	// from. It is what the restart-only comparison is against: comparing the
	// file to itself would find no changes, and comparing it to nothing would
	// report every setting as changed.
	//
	// Touched only from the signal goroutine, which handles one reload at a
	// time.
	current *config.Config
}

// reload re-reads the file and applies what it can.
//
// A configuration that does not parse or does not validate leaves the running
// service exactly as it was. Reloading is not a reason to stop delivering, and
// an operator who has just made a typo should not discover it as an outage.
func (r *reloader) reload(ctx context.Context) {
	r.log.Info("reload: re-reading the configuration", slog.String("path", r.path))

	fresh, err := config.Load(r.path)
	if err != nil {
		r.log.Error("reload: the configuration could not be read; the running one is unchanged",
			slog.String("error", err.Error()))
		return
	}

	if err := r.apply(ctx, fresh); err != nil {
		r.log.Error("reload: the configuration was refused; the running one is unchanged",
			slog.String("error", err.Error()))
		return
	}

	if restart := restartOnly(r.current, fresh); len(restart) > 0 {
		r.log.Warn("reload: some settings need a restart and were not applied",
			slog.String("settings", strings.Join(restart, ", ")),
			slog.String("note", "everything else in the file is now in effect"),
		)
	}

	r.current = fresh
	r.log.Info("reload: done")
}

// apply pushes the reloadable settings into the running components.
func (r *reloader) apply(ctx context.Context, cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	keys, err := cfg.Keys()
	if err != nil {
		return err
	}

	// Log level first, so everything after this is reported at the level the
	// operator just asked for.
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(cfg.Log.Level))); err != nil {
		level = slog.LevelInfo
	}
	r.level.Set(level)

	r.live.SetKeys(keys)
	r.live.SetHandlerTimeout(cfg.Timeouts.Handler.Std())
	r.router.SetDeliverTimeout(cfg.Timeouts.Deliver.Std())
	r.worker.SetPolicy(retryPolicy(cfg.Retry))

	if r.breakers != nil {
		r.breakers.SetSettings(breaker.Settings{
			FailureThreshold: cfg.Breaker.FailureThreshold,
			SuccessThreshold: cfg.Breaker.SuccessThreshold,
			OpenTimeout:      cfg.Breaker.OpenTimeout.Std(),
			HalfOpenProbes:   cfg.Breaker.HalfOpenProbes,
		})
	}

	// Channels come from the database, not from the file: the file seeded it
	// once and the database has been the truth since. Re-reading it here is
	// what applies an edit made directly with a SQLite client — which is a
	// supported way to fix a channel when the UI is the thing that is broken.
	configured, err := r.channels.Load(ctx)
	if err != nil {
		return fmt.Errorf("reading the stored channels: %w", err)
	}
	if err := r.router.Reload(configured); err != nil {
		return fmt.Errorf("rebuilding the channel instances: %w", err)
	}

	return nil
}

// restartOnly lists the settings that changed but cannot be applied live.
//
// It is reported rather than silently ignored because the alternative is an
// operator who edited the listen address, saw "reload: done", and spent the
// next hour wondering why nothing is listening there.
func restartOnly(before, after *config.Config) []string {
	var out []string

	add := func(name string, differs bool) {
		if differs {
			out = append(out, name)
		}
	}

	add("server.addr", before.Server.Addr != after.Server.Addr)
	add("storage.driver", before.Storage.Driver != after.Storage.Driver)
	add("storage.path", before.Storage.Path != after.Storage.Path)
	add("storage.spool_dir", before.Storage.SpoolDir != after.Storage.SpoolDir)
	add("queue.workers", before.Queue.Workers != after.Queue.Workers)
	add("queue.batch", before.Queue.Batch != after.Queue.Batch)
	add("queue.poll_every", before.Queue.PollEvery != after.Queue.PollEvery)
	add("queue.claim_timeout", before.Queue.ClaimTimeout != after.Queue.ClaimTimeout)
	add("queue.recover_every", before.Queue.RecoverEvery != after.Queue.RecoverEvery)
	add("queue.prune_every", before.Queue.PruneEvery != after.Queue.PruneEvery)
	add("queue.sent_retention", before.Queue.SentRetention != after.Queue.SentRetention)
	add("queue.failed_retention", before.Queue.FailedRetention != after.Queue.FailedRetention)
	add("queue.idempotency_retention", before.Queue.IdempotencyRetention != after.Queue.IdempotencyRetention)
	add("queue.release_delay", before.Queue.ReleaseDelay != after.Queue.ReleaseDelay)
	add("smtp_in", !reflect.DeepEqual(before.SMTPIn, after.SMTPIn))
	add("admin", !reflect.DeepEqual(before.Admin, after.Admin))
	add("secret_key", before.SecretKey != after.SecretKey)

	return out
}
