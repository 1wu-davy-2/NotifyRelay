package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "notifyrelay/internal/channel/all"
	"notifyrelay/internal/api"
	"notifyrelay/internal/auth"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/config"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/router"
	"notifyrelay/internal/store/sqlite"
)

// reloadHarness is a running service, minus the listeners.
type reloadHarness struct {
	path     string
	dir      string
	reloader *reloader
	level    *slog.LevelVar
	live     *api.Live
	router   *router.Router
	worker   *queue.Worker
	breakers *breaker.Manager
}

const testKeyHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// writeConfig renders a configuration into the harness's directory.
//
// It supplies only the storage block, which every test needs and none of them
// vary. Everything else — including the auth block — comes from the body: a
// template that also wrote `auth:` would produce a duplicate mapping key the
// moment a test wanted to rotate one, yaml would refuse to parse it, and the
// reload would abort with the test asserting that nothing changed.
func (h *reloadHarness) writeConfig(t *testing.T, body string) {
	t.Helper()

	full := fmt.Sprintf(`
storage:
  driver: sqlite
  path: %q
  spool_dir: %q
%s
`, filepath.Join(h.dir, "x.db"), filepath.Join(h.dir, "spool"), body)

	if err := os.WriteFile(h.path, []byte(full), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// authBlock is the API key every test starts from.
func authBlock(name, hash string) string {
	return fmt.Sprintf("auth:\n  api_keys:\n    - name: %s\n      key_hash: %q\n", name, hash)
}

func newReloadHarness(t *testing.T, body string) *reloadHarness {
	t.Helper()

	dir := t.TempDir()
	h := &reloadHarness{dir: dir, path: filepath.Join(dir, "notifyrelay.yaml")}
	h.writeConfig(t, body)

	cfg, err := config.Load(h.path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	persistence, err := sqlite.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { persistence.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.level = new(slog.LevelVar)
	h.level.Set(slog.LevelInfo)

	keys, err := cfg.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}

	h.breakers = breaker.NewManager(breaker.DefaultSettings(), persistence, log)
	h.router, err = router.New(router.Options{
		DeliverTimeout: cfg.Timeouts.Deliver.Std(),
		Breaker:        h.breakers,
		Log:            log,
	})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}
	h.live = api.NewLive(keys, cfg.Timeouts.Handler.Std())
	h.worker = queue.New(queue.Options{
		Store: persistence, Router: h.router, Log: log,
		Policy:         retryPolicy(cfg.Retry),
		DeliverTimeout: cfg.Timeouts.Deliver.Std(),
	})

	h.reloader = &reloader{
		path:     h.path,
		level:    h.level,
		live:     h.live,
		router:   h.router,
		worker:   h.worker,
		breakers: h.breakers,
		channels: config.NewChannelSource(persistence, nil, log),
		log:      log,
		current:  cfg,
	}
	return h
}

func defaultBody() string {
	return `
server: {addr: ":18080"}
log: {level: info, format: text}
` + authBlock("first", testKeyHash) + `
retry:
  max_attempts: 2
  backoff: [1s, 1s]
  max_age: 1h
`
}

// The acceptance criterion: change the retry count, send SIGHUP, and the
// running worker is using it.
func TestReload_AppliesTheRetryPolicy(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	if got := h.worker.Policy().MaxAttempts; got != 2 {
		t.Fatalf("starting policy = %d attempts, want 2", got)
	}

	h.writeConfig(t, `
server: {addr: ":18080"}
log: {level: info, format: text}
`+authBlock("first", testKeyHash)+`
retry:
  max_attempts: 7
  backoff: [5s, 30s]
  max_age: 2h
`)
	h.reloader.reload(context.Background())

	policy := h.worker.Policy()
	if policy.MaxAttempts != 7 {
		t.Errorf("attempts = %d, want 7 — the change did not reach the worker", policy.MaxAttempts)
	}
	if policy.MaxAge != 2*time.Hour {
		t.Errorf("max_age = %s, want 2h", policy.MaxAge)
	}
	if len(policy.Backoff) != 2 || policy.Backoff[1] != 30*time.Second {
		t.Errorf("backoff = %v, want [5s 30s]", policy.Backoff)
	}
}

// A file that does not parse must leave the running service exactly as it was.
// Reloading is not a reason to stop delivering, and an operator who has just
// made a typo should not discover it as an outage.
func TestReload_ABrokenFileChangesNothing(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	before := h.worker.Policy()

	if err := os.WriteFile(h.path, []byte("this: [is not: valid: yaml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	h.reloader.reload(context.Background())

	if got := h.worker.Policy(); got.MaxAttempts != before.MaxAttempts {
		t.Errorf("a broken file changed the policy: %d -> %d", before.MaxAttempts, got.MaxAttempts)
	}
}

// A file that parses but does not validate is refused for the same reason.
func TestReload_AnInvalidFileChangesNothing(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	before := h.worker.Policy().MaxAttempts

	// retry.max_attempts must be at least 1, and timeouts.handler must exceed
	// timeouts.deliver. Both are checked by Validate.
	h.writeConfig(t, `
server: {addr: ":18080"}
`+authBlock("first", testKeyHash)+`
timeouts: {handler: 1s, deliver: 30s}
retry: {max_attempts: 0, max_age: 1h}
`)
	h.reloader.reload(context.Background())

	if got := h.worker.Policy().MaxAttempts; got != before {
		t.Errorf("an invalid file changed the policy: %d -> %d", before, got)
	}
}

// Rotating a leaked key is the thing an operator should least have to restart a
// service to do.
func TestReload_RotatesAPIKeys(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	if len(h.live.Keys()) != 1 || h.live.Keys()[0].Name != "first" {
		t.Fatalf("starting keys = %+v", h.live.Keys())
	}

	const rotated = "a-freshly-rotated-token"
	h.writeConfig(t, `
server: {addr: ":18080"}
log: {level: info, format: text}
`+authBlock("rotated", auth.HashAPIKey(rotated))+`
retry: {max_attempts: 2, max_age: 1h}
`)
	h.reloader.reload(context.Background())

	keys := h.live.Keys()
	if len(keys) != 1 || keys[0].Name != "rotated" {
		t.Fatalf("keys after reload = %+v, want the rotated one only", keys)
	}
	if !auth.Verify(keys, rotated) {
		t.Error("the new key does not verify")
	}
}

func TestReload_AppliesTheLogLevel(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	h.writeConfig(t, `
server: {addr: ":18080"}
log: {level: debug, format: text}
`+authBlock("first", testKeyHash)+`
retry: {max_attempts: 2, max_age: 1h}
`)
	h.reloader.reload(context.Background())

	if got := h.level.Level(); got != slog.LevelDebug {
		t.Errorf("level = %v, want debug — an operator debugging an incident should not have to restart", got)
	}
}

func TestReload_AppliesTheTimeouts(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	h.writeConfig(t, `
server: {addr: ":18080"}
log: {level: info, format: text}
`+authBlock("first", testKeyHash)+`
timeouts: {handler: 45s, deliver: 40s}
retry: {max_attempts: 2, max_age: 1h}
`)
	h.reloader.reload(context.Background())

	if got := h.router.DeliverTimeout(); got != 40*time.Second {
		t.Errorf("deliver timeout = %s, want 40s", got)
	}
	if got := h.live.HandlerTimeout(); got != 45*time.Second {
		t.Errorf("handler timeout = %s, want 45s", got)
	}
}

// The settings that cannot be applied are reported rather than silently
// ignored: an operator who changed the listen address and read "reload: done"
// would spend the next hour wondering why nothing is listening there.
func TestReload_ReportsSettingsThatNeedARestart(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	before := h.reloader.current

	h.writeConfig(t, `
server: {addr: ":9999"}
log: {level: info, format: text}
`+authBlock("first", testKeyHash)+`
queue: {workers: 16}
retry: {max_attempts: 2, max_age: 1h}
`)
	h.reloader.reload(context.Background())

	after := h.reloader.current
	if before == nil || after == nil {
		t.Fatal("the reloader did not record the configuration")
	}

	restart := restartOnly(before, after)
	want := map[string]bool{"server.addr": true, "queue.workers": true}
	if len(restart) != len(want) {
		t.Fatalf("restart-only = %v, want exactly %v", restart, want)
	}
	for _, name := range restart {
		if !want[name] {
			t.Errorf("unexpected entry %q in %v", name, restart)
		}
	}
}

// Nothing changed means nothing is reported. A warning that fires on every
// reload is a warning nobody reads.
func TestRestartOnly_IsSilentWhenNothingChanged(t *testing.T) {
	cfg := &config.Config{}
	if got := restartOnly(cfg, cfg); len(got) != 0 {
		t.Errorf("restart-only = %v for an unchanged configuration", got)
	}
}

// The loop itself: a signal on the channel runs a reload.
//
// signal.Notify is the one part this cannot cover — Windows has no SIGHUP to
// deliver — so the channel is injected and the five lines around it are what is
// being tested.
func TestWatchForReload_ReloadsOnASignal(t *testing.T) {
	h := newReloadHarness(t, defaultBody())

	h.writeConfig(t, `
server: {addr: ":18080"}
log: {level: info, format: text}
`+authBlock("first", testKeyHash)+`
retry: {max_attempts: 9, max_age: 1h}
`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signals := make(chan os.Signal, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		watchForReload(ctx, h.reloader, signals, h.reloader.log)
	}()

	signals <- syscall.SIGHUP

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && h.worker.Policy().MaxAttempts != 9 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.worker.Policy().MaxAttempts; got != 9 {
		t.Errorf("attempts = %d after a signal, want 9", got)
	}

	cancel()
	wg.Wait()
}

func TestWatchForReload_StopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchForReload(ctx, &reloader{log: log}, make(chan os.Signal), log)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher did not stop when its context was cancelled")
	}
}

// The healthcheck flag exists because the container image is distroless: there
// is no shell and no curl inside it, so a container probe has nothing to run
// except this binary.
func TestRunHealthcheck(t *testing.T) {
	err := runHealthcheck("http://127.0.0.1:1")
	if err == nil {
		t.Fatal("a healthcheck against a closed port succeeded")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("the error does not name the address: %v", err)
	}

	// The address is normally written the way the config writes it, without a
	// scheme, and that has to work too.
	if err := runHealthcheck("127.0.0.1:1"); err == nil {
		t.Error("a bare host:port was not probed")
	}
}
