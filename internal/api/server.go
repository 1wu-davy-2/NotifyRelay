// Package api exposes the HTTP surface.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/config"
	"notifyrelay/internal/metrics"
	"notifyrelay/internal/router"
)

// Deps is what the HTTP layer needs from the rest of the service.
type Deps struct {
	Keys []auth.Key
	Log  *slog.Logger
	// Router delivers notifications; required.
	Router *router.Router
	// HandlerTimeout bounds one whole request, fan-out included. Config
	// validation guarantees it exceeds the per-target delivery timeout.
	HandlerTimeout time.Duration

	// Queue accepts deliveries for asynchronous sending. When nil, the API
	// only offers the synchronous path.
	Queue Enqueuer
	// Store answers delivery queries. When nil, the message endpoints are not
	// registered.
	Store DeliveryReader
	// Ready is pinged by the readiness probe. When nil, readiness is reported
	// without checking anything.
	Ready Pinger
	// Idempotency replays responses for repeated requests. When nil, the
	// Idempotency-Key header is ignored.
	Idempotency IdempotencyStore
	// Metrics, when set, instruments the HTTP layer.
	Metrics *metrics.Metrics
}

// NewHandler builds the HTTP handler with every route registered.
func NewHandler(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(d.Log))

	// Unauthenticated probes. These leak nothing and are what a load balancer
	// or an orchestrator needs to poll.
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.Ready))

	// The metrics endpoint is deliberately unauthenticated: a scraper should
	// not need a credential, and the numbers it exposes are counts, not
	// content. It is bound to whatever address the service is bound to, so an
	// operator who wants it private already knows how.
	if d.Metrics != nil {
		r.Handle("/metrics", d.Metrics.Handler())
	}

	r.Group(func(pr chi.Router) {
		pr.Use(BearerAuth(d.Keys))

		notify := &notifyHandler{
			router:  d.Router,
			queue:   d.Queue,
			records: d.Idempotency,
			log:     d.Log,
			timeout: d.HandlerTimeout,
		}
		if d.Queue == nil {
			// Without a queue there is no asynchronous path; the synchronous
			// one is all this deployment can offer.
			notify.queue = nil
		}
		pr.Post("/api/v1/notify", notify.ServeHTTP)

		pr.Get("/api/v1/channels", (&channelsHandler{
			router: d.Router,
			log:    d.Log,
		}).ServeHTTP)

		if d.Store != nil {
			pr.Get("/api/v1/messages", (&messagesHandler{store: d.Store, log: d.Log}).ServeHTTP)
			pr.Get("/api/v1/messages/{id}", (&messageHandler{store: d.Store, log: d.Log}).ServeHTTP)
		}
	})

	return r
}

// HTTPServer builds the *http.Server with explicit, non-zero timeouts.
//
// Every timeout is set deliberately. A zero value in net/http means "no
// timeout at all", which turns one slow peer into a permanently stuck
// connection — the failure mode this service must not have.
func HTTPServer(cfg config.ServerConfig, to config.TimeoutConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadTimeout:       to.Read.Std(),
		ReadHeaderTimeout: to.ReadHeader.Std(),
		WriteTimeout:      to.Write.Std(),
		IdleTimeout:       to.Idle.Std(),
	}
}

// handleHealthz reports that the process is alive.
//
// It deliberately does not touch the database: a liveness probe that fails
// when a dependency is down gets the process restarted, which fixes nothing
// and loses the queue's in-memory state.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Pinger reports whether a dependency is usable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// handleReadyz reports whether the service can do its job.
//
// This one does check the database: without it the service accepts
// notifications it cannot queue, which looks like success to the caller and
// loses the message.
func handleReadyz(p Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()

			if err := p.Ping(ctx); err != nil {
				WriteError(w, http.StatusServiceUnavailable, "not_ready", "the delivery store is not reachable")
				return
			}
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

// errorBody is the error shape returned by every endpoint.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The header is already sent; the best we can do is stop writing.
		return
	}
}

// WriteError writes a structured error response. code is a stable machine
// token; message is human-facing and must never contain a credential.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorBody{Error: code, Message: message})
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}
