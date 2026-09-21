// Command notifyrelay is the NotifyRelay notification relay server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-smtp"

	// Registering the built-in channel implementations. The blank import runs
	// each channel package's init(), which calls channel.Register. Adding a
	// channel type is one new package plus one line in internal/channel/all.
	_ "notifyrelay/internal/channel/all"

	"notifyrelay/internal/admin"
	"notifyrelay/internal/api"
	"notifyrelay/internal/audit"
	"notifyrelay/internal/auth"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/metrics"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/quota"
	"notifyrelay/internal/router"
	"notifyrelay/internal/secret"
	"notifyrelay/internal/smtpin"
	"notifyrelay/internal/store"
	"notifyrelay/internal/store/spool"
	"notifyrelay/internal/store/sqlite"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "notifyrelay: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "configs/notifyrelay.yaml", "path to the configuration file")
	hashKey := flag.String("hash-key", "", "print the config representation of an API key's digest and exit")
	genKey := flag.Bool("gen-key", false, "print a new secret_key and exit")
	hashPassword := flag.String("hash-password", "", "print an admin password_hash and exit")
	sealValue := flag.String("seal-value", "", "seal a value with secret_key from the configuration file and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("notifyrelay %s\n", version)
		return nil
	}
	if *hashKey != "" {
		fmt.Println(auth.HashAPIKey(*hashKey))
		return nil
	}
	if *genKey {
		_, encoded, err := secret.GenerateKey()
		if err != nil {
			return err
		}
		fmt.Println(encoded)
		return nil
	}
	if *hashPassword != "" {
		// Read from a flag rather than a prompt: this runs on a workstation
		// while preparing a configuration file, and a value on the command line
		// ends up in the shell history either way. What matters is that the
		// *hash* is what gets written down.
		encoded, err := auth.HashPassword(*hashPassword)
		if err != nil {
			return err
		}
		fmt.Println(encoded)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	if *sealValue != "" {
		cipher, err := cfg.Cipher()
		if err != nil {
			return err
		}
		if cipher == nil {
			return errors.New("secret_key is not set in the configuration file")
		}
		sealed, err := cipher.Seal(*sealValue)
		if err != nil {
			return err
		}
		fmt.Println(sealed)
		return nil
	}

	log := newLogger(cfg.Log)

	// Fail fast when a channel type this binary does not have is configured,
	// rather than silently skipping the instance and dropping notifications.
	for _, c := range cfg.Channels {
		if !c.IsEnabled() {
			continue
		}
		if !channel.IsRegistered(c.Type) {
			return fmt.Errorf("channel %q: unknown type %q (registered types: %v)",
				c.Name, c.Type, channel.Registered())
		}
	}

	keys, err := cfg.Keys()
	if err != nil {
		return err
	}

	// The signal context is created before the workers so a shutdown stops
	// them too, not only the listeners.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	persistence, err := openStore(cfg.Storage)
	if err != nil {
		return err
	}
	defer persistence.Close()

	bodies, err := spool.New(cfg.Storage.SpoolDir)
	if err != nil {
		return err
	}

	mx := metrics.New()

	recorder := audit.NewSlogRecorder(log)

	var breakers *breaker.Manager
	if cfg.Breaker.Enabled {
		breakers = breaker.NewManager(breaker.Settings{
			FailureThreshold: cfg.Breaker.FailureThreshold,
			SuccessThreshold: cfg.Breaker.SuccessThreshold,
			OpenTimeout:      cfg.Breaker.OpenTimeout.Std(),
			HalfOpenProbes:   cfg.Breaker.HalfOpenProbes,
		}, persistence, log)
	}

	// Channel instances come from the database, seeded once from the file. The
	// file's `channels:` block is read only while the database has never been
	// configured; see config.ChannelSource for why that rule has to be a
	// recorded fact rather than an inference.
	cipher, err := cfg.Cipher()
	if err != nil {
		return err
	}
	channelSource := config.NewChannelSource(persistence, cipher, log)

	if _, err := channelSource.ImportOnce(ctx, cfg.Channels); err != nil {
		return err
	}
	configured, err := channelSource.Load(ctx)
	if err != nil {
		return err
	}

	rtr, err := router.New(router.Options{
		Channels:       configured,
		DeliverTimeout: cfg.Timeouts.Deliver.Std(),
		Audit:          recorder,
		Breaker:        breakers,
		Quota:          quota.New(persistence),
		Log:            log,
	})
	if err != nil {
		return err
	}

	worker := queue.New(queue.Options{
		Store:                  persistence,
		Spool:                  bodies,
		Router:                 rtr,
		Policy:                 retryPolicy(cfg.Retry),
		Log:                    log,
		Metrics:                mx,
		Workers:                cfg.Queue.Workers,
		Batch:                  cfg.Queue.Batch,
		PollEvery:              cfg.Queue.PollEvery.Std(),
		DeliverTimeout:         cfg.Queue.DeliverTimeout.Std(),
		ReleaseDelay:           cfg.Queue.ReleaseDelay.Std(),
		ClaimTimeout:           cfg.Queue.ClaimTimeout.Std(),
		RecoverEvery:           cfg.Queue.RecoverEvery.Std(),
		PruneEvery:             cfg.Queue.PruneEvery.Std(),
		SentRetention:          cfg.Queue.SentRetention.Std(),
		FailedRetention:        cfg.Queue.FailedRetention.Std(),
		IdempotencyRetention:   cfg.Queue.IdempotencyRetention.Std(),
	})

	// The operator surface, when configured. It is built before the API handler
	// because the API mounts it; when disabled the constructor returns nil and
	// nothing is mounted.
	adminHandler := admin.NewHandler(admin.Deps{
		Config:   cfg.Admin,
		Channels: channelSource,
		Breakers: breakers,
		Router:   rtr,
		Audit:    persistence,
		Log:      log,
	})

	handler := api.NewHandler(api.Deps{
		Admin:          adminHandler,
		Keys:           keys,
		Log:            log,
		Router:         rtr,
		HandlerTimeout: cfg.Timeouts.Handler.Std(),
		Queue:          worker,
		Store:          persistence,
		Ready:          persistence,
		Idempotency:    persistence,
		Metrics:        mx,
	})
	srv := api.HTTPServer(cfg.Server, cfg.Timeouts, handler)

	log.Info("starting",
		slog.String("version", version),
		slog.String("http_addr", cfg.Server.Addr),
		slog.Bool("smtp_inbound", cfg.SMTPIn.Enabled),
		slog.Any("registered_channel_types", channel.Registered()),
		slog.Any("configured_channels", rtr.Instances()),
		slog.String("storage_driver", cfg.Storage.Driver),
		slog.String("storage_path", cfg.Storage.Path),
		slog.Int("queue_workers", cfg.Queue.Workers),
		slog.String("timeout_handler", cfg.Timeouts.Handler.String()),
		slog.String("timeout_deliver", cfg.Timeouts.Deliver.String()),
	)
	if len(rtr.Instances()) == 0 {
		log.Warn("no channel instances configured; every delivery will fail until channels are added")
	}

	errCh := make(chan error, 1)

	go func() {
		if err := worker.Run(ctx); err != nil {
			errCh <- fmt.Errorf("queue worker: %w", err)
		}
	}()

	// SMTP inbound: any system that can send mail can raise a notification.
	var smtpSrv *smtpin.Server
	if cfg.SMTPIn.Enabled {
		smtpSrv, err = smtpin.New(cfg.SMTPIn, rtr, log)
		if err != nil {
			return err
		}
		go func() {
			log.Info("smtp inbound listening",
				slog.String("addr", cfg.SMTPIn.Addr),
				slog.String("hostname", cfg.SMTPIn.Hostname),
				slog.Bool("auth_required", cfg.SMTPIn.Auth.Enabled),
			)
			if err := smtpSrv.ListenAndServe(); err != nil && !errors.Is(err, smtp.ErrServerClosed) {
				errCh <- fmt.Errorf("smtp server: %w", err)
			}
		}()
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if smtpSrv != nil {
		if err := smtpSrv.Shutdown(shutdownCtx); err != nil {
			log.Warn("smtp shutdown did not complete cleanly", slog.String("error", err.Error()))
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	log.Info("stopped")
	return nil
}

// openStore builds the persistence layer named in the configuration.
//
// The switch is the seam the store package exists for: a MySQL or PostgreSQL
// implementation registers here and nothing above this function changes.
func openStore(cfg config.StorageConfig) (store.Store, error) {
	switch cfg.Driver {
	case "sqlite":
		return sqlite.Open(cfg.Path)
	default:
		return nil, fmt.Errorf("storage.driver %q is not supported", cfg.Driver)
	}
}

func retryPolicy(cfg config.RetryConfig) queue.Policy {
	backoff := make([]time.Duration, 0, len(cfg.Backoff))
	for _, d := range cfg.Backoff {
		backoff = append(backoff, d.Std())
	}
	return queue.Policy{
		MaxAttempts: cfg.MaxAttempts,
		Backoff:     backoff,
		MaxAge:      cfg.MaxAge.Std(),
	}
}

func newLogger(cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.TrimSpace(cfg.Level))); err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if strings.EqualFold(strings.TrimSpace(cfg.Format), "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
