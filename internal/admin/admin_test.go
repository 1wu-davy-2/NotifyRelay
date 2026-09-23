package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/auth"
	"notifyrelay/internal/breaker"
	"notifyrelay/internal/channel"
	_ "notifyrelay/internal/channel/all"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/queue"
	"notifyrelay/internal/router"
	"notifyrelay/internal/secret"
	"notifyrelay/internal/store"
	"notifyrelay/internal/store/sqlite"

	"github.com/go-chi/chi/v5"
)

const (
	testUser     = "operator"
	testPassword = "correct horse battery staple"
)

// cheapHash keeps the argon2 cost out of the test runtime. The parameters
// travel inside the encoded hash, so the handler verifies it with these rather
// than with the production defaults.
func cheapHash(t *testing.T, password string) string {
	t.Helper()
	encoded, err := auth.HashPasswordWith(password, auth.Argon2Params{
		Memory: 64, Time: 1, Parallelism: 1, SaltLength: 8, KeyLength: 16,
	})
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	return encoded
}

type harness struct {
	handler  http.Handler
	store    *sqlite.Store
	source   *config.ChannelSource
	breakers *breaker.Manager
	router   *router.Router
	queue    *recordingQueue
}

// recordingQueue stands in for the delivery queue.
//
// It records what it was handed rather than delivering, because what this
// package has to get right is the message it puts on the queue and the target
// it names — the queue and the router have their own tests for the rest. The
// delivery it returns carries an id so the caller can be sent to a page.
type recordingQueue struct {
	mu       sync.Mutex
	enqueued []enqueuedCall
	err      error
}

type enqueuedCall struct {
	RequestID string
	Message   *message.Message
	Targets   []queue.TargetSpec
}

func (q *recordingQueue) Enqueue(_ context.Context, requestID string, msg *message.Message, targets []queue.TargetSpec) ([]*store.Delivery, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.err != nil {
		return nil, q.err
	}
	q.enqueued = append(q.enqueued, enqueuedCall{RequestID: requestID, Message: msg, Targets: targets})

	out := make([]*store.Delivery, 0, len(targets))
	for i, t := range targets {
		out = append(out, &store.Delivery{
			ID:        fmt.Sprintf("d-test-%d", i),
			RequestID: requestID,
			Target:    t.Target,
		})
	}
	return out, nil
}

func (q *recordingQueue) calls() []enqueuedCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]enqueuedCall(nil), q.enqueued...)
}

func newHarness(t *testing.T, withKey bool) *harness {
	t.Helper()
	return newHarnessWith(t, withKey, nil)
}

// newUnclaimedHarness builds a deployment that has no administrator yet.
//
// That is the first-run state: the operator UI is on, no password hash is
// configured, and nothing has been written to the credential table. It is the
// state `docker compose up -d` produces, and the one the setup page exists for.
func newUnclaimedHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, true, &config.AdminConfig{
		Enabled:    true,
		SessionKey: "session-key-value",
		SessionTTL: config.Duration(time.Hour),
	})
}

func newHarnessWith(t *testing.T, withKey bool, adminCfg *config.AdminConfig) *harness {
	t.Helper()

	persistence, err := sqlite.Open(filepath.Join(t.TempDir(), "admin.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { persistence.Close() })

	var cipher *secret.Cipher
	if withKey {
		key, _, err := secret.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if cipher, err = secret.NewCipher(key); err != nil {
			t.Fatalf("NewCipher: %v", err)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	source := config.NewChannelSource(persistence, cipher, log)

	// The router is the real one, started empty, because the acceptance
	// criterion is that a channel created through this API becomes live in it
	// without a restart.
	rtr, err := router.New(router.Options{
		DeliverTimeout: time.Second,
		Breaker:        breaker.NewManager(breaker.DefaultSettings(), persistence, log),
		Log:            log,
	})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}

	breakers := breaker.NewManager(breaker.DefaultSettings(), persistence, log)

	if adminCfg == nil {
		adminCfg = &config.AdminConfig{
			Enabled:      true,
			Username:     testUser,
			PasswordHash: cheapHash(t, testPassword),
			SessionKey:   "session-key-value",
			SessionTTL:   config.Duration(time.Hour),
		}
	}

	queue := &recordingQueue{}

	h := NewHandler(Deps{
		Config:      *adminCfg,
		Auth:        config.AuthConfig{},
		Channels:    source,
		Credentials: persistence,
		Keys:        persistence,
		Breakers:    breakers,
		Router:      rtr,
		Deliveries:  persistence,
		Queue:       queue,
		Audit:       persistence,
		Log:         log,
	})
	if h == nil {
		t.Fatal("NewHandler returned nil for an enabled admin block")
	}

	// Mounted the way api.NewHandler mounts it, so the tests exercise the real
	// paths rather than the sub-router's own idea of them. A test that calls
	// the handler directly would pass on "/channels" and prove nothing about
	// where the routes actually live.
	root := chi.NewRouter()
	root.Mount("/admin", h)

	return &harness{
		handler: root, store: persistence, source: source,
		breakers: breakers, router: rtr, queue: queue,
	}
}

// ---------------------------------------------------------------- request kit

type response struct {
	code   int
	body   []byte
	cookie *http.Cookie
	// header carries the response headers, which is where a redirect's
	// destination lives — the thing a first-run test has to assert on.
	header http.Header
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %q: %v", r.body, err)
	}
}

func (r response) text() string { return string(r.body) }

// do sends a request, optionally with a session cookie and the CSRF header.
func (h *harness) do(t *testing.T, method, path string, body any, cookie *http.Cookie, csrf bool) response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "203.0.113.7:54321"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf {
		req.Header.Set(csrfHeader, "1")
	}

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	out := response{code: rec.Code, body: rec.Body.Bytes(), header: rec.Result().Header}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			out.cookie = c
		}
	}
	return out
}

// signIn logs in and returns the session cookie.
func (h *harness) signIn(t *testing.T) *http.Cookie {
	t.Helper()

	res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: testPassword}, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("login: status %d: %s", res.code, res.text())
	}
	if res.cookie == nil {
		t.Fatal("login set no session cookie")
	}
	return res.cookie
}

// --------------------------------------------------------------------- login

func TestLogin_AcceptsTheRightPasswordAndRejectsTheRest(t *testing.T) {
	h := newHarness(t, true)

	res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: testPassword}, nil, false)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.code, res.text())
	}

	cookie := res.cookie
	if cookie == nil || cookie.Value == "" {
		t.Fatal("no session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/admin" {
		t.Errorf("Path = %q, want /admin", cookie.Path)
	}
}

// The response must not say which half was wrong. Telling them apart tells an
// attacker which half they already have.
func TestLogin_DoesNotRevealWhichHalfWasWrong(t *testing.T) {
	h := newHarness(t, true)

	wrongPassword := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: "nope"}, nil, false)
	wrongUser := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: "someone-else", Password: testPassword}, nil, false)

	if wrongPassword.code != http.StatusUnauthorized || wrongUser.code != http.StatusUnauthorized {
		t.Fatalf("codes = %d and %d, want 401 for both", wrongPassword.code, wrongUser.code)
	}
	if wrongPassword.text() != wrongUser.text() {
		t.Errorf("the two failures are distinguishable:\n  %s\n  %s", wrongPassword.text(), wrongUser.text())
	}
}

// Guessing has to become pointless, or the operator password is the only thing
// between the internet and every channel this service talks to.
func TestLogin_ThrottlesRepeatedFailures(t *testing.T) {
	h := newHarness(t, true)

	var throttled bool
	for i := 0; i < freeAttempts+3; i++ {
		res := h.do(t, http.MethodPost, "/admin/api/login",
			loginRequest{Username: testUser, Password: "wrong"}, nil, false)
		if res.code == http.StatusTooManyRequests {
			throttled = true
			// The code, not the message. The message is copy and it is now in the
			// language the caller asked for; the code is the contract, and it is
			// what a client is supposed to branch on.
			if !strings.Contains(res.text(), `"error":"too_many_attempts"`) {
				t.Errorf("throttled response = %s", res.text())
			}
			break
		}
	}
	if !throttled {
		t.Fatalf("no throttle after %d failed attempts", freeAttempts+3)
	}

	// And the correct password does not get through either while the delay
	// stands — otherwise the throttle is decorative.
	res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: testPassword}, nil, false)
	if res.code != http.StatusTooManyRequests {
		t.Errorf("status = %d during the backoff, want 429", res.code)
	}
}

func TestLogin_ASuccessfulSignInClearsTheThrottle(t *testing.T) {
	h := newHarness(t, true)

	for i := 0; i < freeAttempts-1; i++ {
		h.do(t, http.MethodPost, "/admin/api/login",
			loginRequest{Username: testUser, Password: "wrong"}, nil, false)
	}
	if res := h.do(t, http.MethodPost, "/admin/api/login",
		loginRequest{Username: testUser, Password: testPassword}, nil, false); res.code != http.StatusOK {
		t.Fatalf("status = %d after a few mistakes: %s", res.code, res.text())
	}

	// A fresh run of mistakes starts from zero.
	for i := 0; i < freeAttempts-1; i++ {
		res := h.do(t, http.MethodPost, "/admin/api/login",
			loginRequest{Username: testUser, Password: "wrong"}, nil, false)
		if res.code == http.StatusTooManyRequests {
			t.Fatal("the failure count was not cleared by the successful sign-in")
		}
	}
}

// --------------------------------------------------------------- the session

func TestProtectedRoutes_RequireASession(t *testing.T) {
	h := newHarness(t, true)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/admin/api/session"},
		{http.MethodGet, "/admin/api/channels"},
		{http.MethodGet, "/admin/api/channels/types"},
		{http.MethodGet, "/admin/api/audit"},
		{http.MethodDelete, "/admin/api/channels/anything"},
	} {
		res := h.do(t, tc.method, tc.path, nil, nil, true)
		if res.code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d without a session, want 401", tc.method, tc.path, res.code)
		}
	}
}

func TestProtectedRoutes_RejectAForgedCookie(t *testing.T) {
	h := newHarness(t, true)

	res := h.do(t, http.MethodGet, "/admin/api/channels", nil,
		&http.Cookie{Name: cookieName, Value: "not-a-real-session-id"}, true)
	if res.code != http.StatusUnauthorized {
		t.Errorf("status = %d with a made-up session id, want 401", res.code)
	}
}

// SameSite stops a cross-site POST from carrying the cookie. This is the second
// lock on the same door, for the browser that does not honour the first.
func TestStateChangingRequests_RequireTheCSRFHeader(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/channels",
		saveRequest{Name: "x", Type: "email", Config: map[string]any{}}, cookie, false)
	if res.code != http.StatusForbidden {
		t.Errorf("status = %d without the CSRF header, want 403", res.code)
	}

	// Reads do not need it: requiring it everywhere would mean the UI had to
	// send it on navigations it does not control.
	if res := h.do(t, http.MethodGet, "/admin/api/channels", nil, cookie, false); res.code != http.StatusOK {
		t.Errorf("a read was refused without the CSRF header: %d", res.code)
	}
}

func TestLogout_EndsTheSessionServerSide(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if res := h.do(t, http.MethodPost, "/admin/api/logout", nil, cookie, true); res.code != http.StatusOK {
		t.Fatalf("logout: status %d", res.code)
	}

	// The cookie is still in the test's hand, as a copy taken from a shared
	// machine would be. It must not work any more — that is the whole reason
	// sessions are server-side.
	res := h.do(t, http.MethodGet, "/admin/api/session", nil, cookie, true)
	if res.code != http.StatusUnauthorized {
		t.Errorf("status = %d after signing out, want 401", res.code)
	}
}

func TestSessions_Expire(t *testing.T) {
	s := newSessions(time.Minute)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }

	id, err := s.create("operator")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, ok := s.lookup(id); !ok {
		t.Fatal("a fresh session did not resolve")
	}

	now = now.Add(2 * time.Minute)
	if _, ok := s.lookup(id); ok {
		t.Error("an expired session still resolves")
	}
	if n := s.count(); n != 0 {
		t.Errorf("%d sessions survived their expiry", n)
	}
}

// ------------------------------------------------------------------ channels

func emailConfig(password string) map[string]any {
	cfg := map[string]any{
		"host": "smtp.example.com",
		"from": "notify@example.com",
		"to":   []any{"ops@example.com"},
	}
	if password != "" {
		cfg["username"] = "notify@example.com"
		cfg["password"] = password
	}
	return cfg
}

func TestChannels_CreateAndRead(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("save: status %d: %s", res.code, res.text())
	}

	var view channelView
	res.decode(t, &view)
	if view.Name != "oncall" || view.Type != "email" {
		t.Errorf("saved view = %+v", view)
	}
	if !view.Live {
		t.Error("the new channel is not live; it will not receive deliveries")
	}

	// The credential is not in the response, and the response says it is set.
	if _, present := view.Config["password"]; present {
		t.Errorf("the password was returned to the client: %+v", view.Config)
	}
	if len(view.SecretsSet) != 1 || view.SecretsSet[0] != "password" {
		t.Errorf("secrets_set = %v, want [password]", view.SecretsSet)
	}
	if view.Config["host"] != "smtp.example.com" {
		t.Errorf("host = %v, want it returned — it is not a secret", view.Config["host"])
	}
}

// The acceptance criterion for the whole admin surface: a channel created here
// is delivering without a restart.
func TestChannels_CreateTakesEffectWithoutARestart(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if got := h.router.Instances(); len(got) != 0 {
		t.Fatalf("the router started with %v", got)
	}

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	if got := h.router.Instances(); len(got) != 1 || got[0] != "oncall" {
		t.Errorf("router instances = %v, want [oncall] with no restart", got)
	}
	if typ := h.router.TypeOf("oncall"); typ != "email" {
		t.Errorf("TypeOf(oncall) = %q, want email", typ)
	}
}

func TestChannels_Delete(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	res := h.do(t, http.MethodDelete, "/admin/api/channels/oncall", nil, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("delete: status %d: %s", res.code, res.text())
	}
	if got := h.router.Instances(); len(got) != 0 {
		t.Errorf("router still holds %v after a delete", got)
	}

	// Deleting it twice is a 404, not a second success.
	if res := h.do(t, http.MethodDelete, "/admin/api/channels/oncall", nil, cookie, true); res.code != http.StatusNotFound {
		t.Errorf("second delete: status %d, want 404", res.code)
	}
}

// The property that makes an edit form possible: a form cannot send back a
// credential it was never shown, so an absent private parameter must mean
// "unchanged" rather than "clear".
func TestChannels_EditKeepsTheCredentialItWasNotGiven(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	// An edit that touches the host and says nothing about the password, which
	// is exactly what the form sends. The username stays: email requires the
	// two together, and dropping one while keeping the other is a configuration
	// the service refuses — which is its own test below.
	edited := emailConfig("")
	edited["username"] = "notify@example.com"
	edited["host"] = "smtp2.example.com"

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: edited,
	}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("edit: status %d: %s", res.code, res.text())
	}

	stored, err := h.source.Get(context.Background(), "oncall")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Config["password"] != "hunter2" {
		t.Errorf("password = %v, want it kept — the edit did not mention it", stored.Config["password"])
	}
	if stored.Config["host"] != "smtp2.example.com" {
		t.Errorf("host = %v, want the edit applied", stored.Config["host"])
	}
}

// ...and an explicit empty string does clear it, or there would be no way to
// remove a credential at all.
func TestChannels_EditCanClearACredential(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	cleared := emailConfig("")
	cleared["password"] = ""
	cleared["username"] = ""

	if res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: cleared,
	}, cookie, true); res.code != http.StatusOK {
		t.Fatalf("edit: status %d: %s", res.code, res.text())
	}

	stored, err := h.source.Get(context.Background(), "oncall")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := stored.Config["password"]; got != nil && got != "" {
		t.Errorf("password = %v, want it cleared", got)
	}
}

// A configuration the service cannot run must be refused while the operator is
// looking at the form, not stored and discovered at the next restart.
func TestChannels_RejectAConfigurationThatWouldNotStart(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	for name, req := range map[string]saveRequest{
		"unknown type": {Name: "x", Type: "no-such-type", Config: map[string]any{}},
		"unknown key": {Name: "x", Type: "email", Config: map[string]any{
			"host": "h", "from": "f@e.com", "to": []any{"o@e.com"}, "hots": "typo",
		}},
		"missing required": {Name: "x", Type: "email", Config: map[string]any{
			"host": "h",
		}},
		"out of range": {Name: "x", Type: "email", Config: map[string]any{
			"host": "h", "from": "f@e.com", "to": []any{"o@e.com"}, "port": 70000,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			res := h.do(t, http.MethodPost, "/admin/api/channels", req, cookie, true)
			if res.code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400: %s", res.code, res.text())
			}
		})
	}

	// Nothing was stored along the way.
	stored, err := h.source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("%d invalid channels were stored", len(stored))
	}
}

// A credential that cannot be sealed must not be stored in the clear.
func TestChannels_RefuseACredentialWithoutAKey(t *testing.T) {
	h := newHarness(t, false)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)
	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.code, res.text())
	}
	if !strings.Contains(res.text(), "secret_key") {
		t.Errorf("the error does not name the setting: %s", res.text())
	}
}

func TestChannelTypes_ServesTheSchemaTheFormIsGeneratedFrom(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodGet, "/admin/api/channels/types", nil, cookie, false)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d", res.code)
	}

	var out struct {
		Channels []struct {
			Type       string `json:"type"`
			Parameters []struct {
				Name   string `json:"name"`
				ShowIf *struct {
					Field  string `json:"field"`
					Equals any    `json:"equals"`
				} `json:"show_if"`
				Min *float64 `json:"min"`
			} `json:"parameters"`
		} `json:"channels"`
	}
	res.decode(t, &out)

	if len(out.Channels) == 0 {
		t.Fatal("no channel types")
	}

	// The form generator needs these, and they are the reason the endpoint
	// exists rather than the UI hardcoding a field list.
	var sawShowIf, sawMin bool
	for _, c := range out.Channels {
		for _, p := range c.Parameters {
			if p.ShowIf != nil {
				sawShowIf = true
			}
			if p.Min != nil {
				sawMin = true
			}
		}
	}
	if !sawShowIf {
		t.Error("no parameter carried show_if; a generated form cannot express conditional fields")
	}
	if !sawMin {
		t.Error("no parameter carried min; a generated form cannot bound a number")
	}
}

// ------------------------------------------------------------------- breaker

func TestBreakerReset_ClosesAnOpenChannelAndIsAudited(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	// Open the breaker.
	b := h.breakers.For("oncall")
	now := time.Now()
	for i := 0; i < breaker.DefaultSettings().FailureThreshold; i++ {
		b.Record(context.Background(), channel.ClassTransient, now)
	}
	if ok, _ := b.Allow(context.Background(), now); ok {
		t.Fatal("the breaker should be open")
	}

	res := h.do(t, http.MethodPost, "/admin/api/channels/oncall/breaker/reset", nil, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("reset: status %d: %s", res.code, res.text())
	}
	if !strings.Contains(res.text(), "open") {
		t.Errorf("the response does not report what was replaced: %s", res.text())
	}

	if ok, _ := b.Allow(context.Background(), now); !ok {
		t.Error("the channel is still refused after a reset")
	}

	// The action is in the audit trail, with the state it overrode.
	actions, err := h.store.ListAdminActions(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAdminActions: %v", err)
	}
	var found bool
	for _, a := range actions {
		if a.Action == "breaker.reset" && a.Target == "oncall" {
			found = true
			if a.Actor != testUser {
				t.Errorf("actor = %q, want %q", a.Actor, testUser)
			}
			if !strings.Contains(a.Detail, "open") {
				t.Errorf("detail = %q, want it to record what was replaced", a.Detail)
			}
		}
	}
	if !found {
		t.Errorf("the reset is not in the audit trail: %+v", actions)
	}
}

func TestBreakerReset_RefusesAnUnknownChannel(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/channels/nope/breaker/reset", nil, cookie, true)
	if res.code != http.StatusNotFound {
		t.Errorf("status = %d for an unconfigured channel, want 404", res.code)
	}
}

// The audit trail must never carry a credential: it is read by more people than
// the configuration is.
func TestAudit_RecordsWhatChangedAndNotTheValues(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	edited := emailConfig("")
	edited["password"] = "a-different-secret"
	edited["username"] = "notify@example.com"

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: edited,
	}, cookie, true)

	actions, err := h.store.ListAdminActions(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListAdminActions: %v", err)
	}

	var sawUpdate bool
	for _, a := range actions {
		if strings.Contains(a.Detail, "hunter2") || strings.Contains(a.Detail, "a-different-secret") {
			t.Errorf("a credential reached the audit trail: %q", a.Detail)
		}
		if a.Action == "channel.update" {
			sawUpdate = true
			if !strings.Contains(a.Detail, "password") {
				t.Errorf("detail = %q, want it to name the parameter that changed", a.Detail)
			}
		}
	}
	if !sawUpdate {
		t.Errorf("the edit was not audited: %+v", actions)
	}
}

// ------------------------------------------------------------------- disabled

func TestNewHandler_IsNilWhenDisabled(t *testing.T) {
	if h := NewHandler(Deps{Config: config.AdminConfig{Enabled: false}}); h != nil {
		t.Error("a disabled admin block produced a handler")
	}
}

// compile-time check that the sqlite store satisfies what the admin surface
// needs, so a missing method is a build failure rather than a runtime one.
var _ store.AdminAudit = (*sqlite.Store)(nil)

// A change that is stored but cannot be loaded leaves the running router on the
// configuration that works, and says so.
//
// The alternative — refusing the save, or storing a configuration the service
// cannot run — are both worse. Refusing loses the operator's edit; storing it
// silently means the next restart fails to start, and the connection between
// cause and effect is a week apart.
//
// Reaching this state through the API is not possible: a save is validated with
// the same two calls the router makes, so anything storable is loadable. The
// reachable route is a row that was already there — written by hand, or left by
// a build whose schema was looser — and it surfaces on the next save of any
// channel, which is why the response has to distinguish the two.
func TestChannels_SavedButNotLiveIsReported(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// A row the API would never have accepted, written straight to the store.
	if err := h.store.PutChannel(context.Background(), &store.ChannelInstance{
		Name:    "legacy",
		Type:    "email",
		Enabled: true,
		Config:  map[string]any{"host": "smtp.example.com", "hots": "typo"},
	}); err != nil {
		t.Fatalf("PutChannel: %v", err)
	}

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	if res.code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", res.code, res.text())
	}

	var out struct {
		Saved  bool   `json:"saved"`
		Live   bool   `json:"live"`
		Reload string `json:"reload"`
	}
	res.decode(t, &out)

	if !out.Saved {
		t.Error("the edit was not stored")
	}
	if out.Live {
		t.Error("the response claims the channel is live")
	}
	if !strings.Contains(out.Reload, "legacy") {
		t.Errorf("reload = %q, want it to name the channel that broke the load", out.Reload)
	}

	// Nothing is live: the reload is all-or-nothing, so the router keeps the
	// set it had rather than adopting half of a broken configuration.
	if got := h.router.Instances(); len(got) != 0 {
		t.Errorf("router instances = %v, want the previous set left alone", got)
	}

	// And the stored edit survives, so fixing the other channel and saving
	// again is all it takes.
	stored, err := h.source.Get(context.Background(), "oncall")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored == nil {
		t.Fatal("the edit was not stored")
	}
}

// The channel view tells the operator which instances are actually loaded,
// which is how a failed reload is noticed after the fact rather than only in
// the response to the save.
func TestChannels_ViewReportsWhetherTheChannelIsLive(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	res := h.do(t, http.MethodGet, "/admin/api/channels/oncall", nil, cookie, false)
	var view channelView
	res.decode(t, &view)
	if !view.Live {
		t.Error("a saved channel reports itself as not live")
	}

	// A disabled channel exists but is not loaded, and the two must be
	// distinguishable — otherwise "off on purpose" and "broken" look the same.
	disabled := false
	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "spare", Type: "email", Enabled: &disabled, Config: emailConfig("hunter2"),
	}, cookie, true)

	res = h.do(t, http.MethodGet, "/admin/api/channels/spare", nil, cookie, false)
	res.decode(t, &view)
	if view.Enabled {
		t.Error("a disabled channel came back enabled")
	}
	if view.Live {
		t.Error("a disabled channel reports itself as live")
	}
}

// The request types are decoded with DisallowUnknownFields, so a field the
// client sends that the struct does not name is an error rather than something
// silently ignored. That is the right default — a typo'd quota should not
// quietly become "unlimited" — and it means the json tags have to agree with
// what the UI sends.
//
// This test goes through real JSON rather than building the Go struct, which is
// the only way to catch a missing tag: constructing a QuotaConfig in Go works
// perfectly whether or not it has one, and the live server rejected every
// request that carried a quota until it did.
func TestSaveChannel_AcceptsTheJSONTheUIActuallySends(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// Written out as a string, exactly as a browser would post it.
	const body = `{
		"name": "sink",
		"type": "webhook",
		"enabled": true,
		"config": {"url": "https://example.com/notify", "auth_type": "none"},
		"quota": {"per_second": 5, "per_minute": 100, "per_hour": 1000,
		          "per_day": 5000, "per_month": 100000}
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/channels", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	stored, err := h.source.Get(context.Background(), "sink")
	if err != nil || stored == nil {
		t.Fatalf("Get: %v", err)
	}

	want := config.QuotaConfig{PerSecond: 5, PerMinute: 100, PerHour: 1000, PerDay: 5000, PerMonth: 100000}
	if stored.Quota != want {
		t.Errorf("quota = %+v, want %+v — a quota the UI cannot set is a quota nobody sets", stored.Quota, want)
	}
	if stored.Config["url"] != "https://example.com/notify" {
		t.Errorf("url = %v", stored.Config["url"])
	}
}

// An integer parameter sent as a string is refused, and the client has to know.
//
// ---------------------------------------------------------------------------
// This is a contract test, in the shape of
// TestChannelFormKeepsTheDefaultsAJSONClientCannotSee: it pins the half of a
// rule that lives on this side so that the other half has somewhere to point.
//
// Every control in an HTML form holds text. The form renders an integer
// parameter into a text box, so its rendered default is the string "0" — and
// the server reads it with a type assertion and refuses a string. The client is
// the only thing that can turn it back into a number, and for a while nothing
// did: `collect()` posted what the box held, verbatim.
//
// The result was not a rough edge. Every channel type with a numeric parameter
// refused to save with its own defaults untouched, which made webhook, slack
// and the rest unusable from the interface while the API accepted the same
// configuration happily — and the error named three parameters the operator had
// not touched and could not have got wrong.
//
// It survived the test that was supposed to catch it, because that test
// hand-writes a body with no numeric parameter in it — written by somebody who
// already knew what the server wanted, which is the knowledge the form is
// supposed to encode. See TestChannelForm_AnEditRoundTrips for the version that
// reads the form's own output instead.
//
// That round trip would not have caught this one either, and it is worth being
// precise about why: it builds its body in Go, where a number is already a
// number. What it catches is a field the form renders in a way it cannot read
// back. This test catches the other half — the client's half — and the only
// thing that can, because the coercion happens in TypeScript. What it does is
// write the rule down where whoever edits collect() will find it.
//
// If the server ever starts accepting a numeric string, this fails — and that
// would be correct: the comment in web/src/lib/schema.ts would have become a
// lie, and somebody should decide which side changes.
// ---------------------------------------------------------------------------
func TestSaveChannel_RefusesANumericParameterSentAsAString(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// The body a client posts when it forwards the form's values without
	// converting them. Two of these are integers and one is a float; all three
	// are strings, which is what the box held.
	const body = `{
		"name": "as-strings",
		"type": "webhook",
		"enabled": true,
		"config": {
			"url": "https://example.com/notify",
			"body_max_len": "0",
			"title_max_len": "0",
			"rate_per_sec": "0"
		},
		"quota": {"per_second": 0, "per_minute": 0, "per_hour": 0, "per_day": 0, "per_month": 0}
	}`

	req := httptest.NewRequest(http.MethodPost, "/admin/api/channels", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — the server now accepts a numeric string, so the "+
			"conversion in web/src/lib/schema.ts is no longer required and its comment is "+
			"wrong: %s", rec.Code, rec.Body.String())
	}

	var refused struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Named per field, because the client places them under the boxes they are
	// about — and a complaint that arrived as one sentence about three
	// parameters would land above the form instead.
	for _, name := range []string{"body_max_len", "title_max_len", "rate_per_sec"} {
		if refused.Fields[name] == "" {
			t.Errorf("no field-level complaint for %s: %s", name, rec.Body.String())
		}
	}
}

// Opening a channel and saving it unchanged changes nothing.
//
// ---------------------------------------------------------------------------
// The property an operator exercises every day, and the one the bug above got
// past: the values the form renders have to be values the server will accept
// back. It is asserted as a round trip rather than against a hand-written body,
// because a hand-written body is written by somebody who already knows what the
// server wants — which is exactly the knowledge the form is supposed to encode.
//
// So this reads the form's own output and posts it straight back. Every field
// that has a rendered value is sent with the type its `kind` says it has, which
// is what collect() in web/src/lib/schema.ts does; the private ones are blank
// and are omitted, which is what "leave the stored credential alone" looks like
// on the wire.
//
// It asserts the stored configuration is unchanged and not merely accepted: a
// field that renders lossily — a list joined and re-split wrong, a duration
// normalised to something else — saves cleanly and quietly rewrites itself.
// ---------------------------------------------------------------------------
func TestChannelForm_AnEditRoundTrips(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	save := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "sink", Type: "webhook",
		Config: map[string]any{
			"url":              "https://example.com/notify",
			"method":           "PUT",
			"auth_type":        "hmac",
			"secret":           "hunter2-signing-secret",
			"signature_prefix": "sha256=",
			"body_max_len":     4096,
			"title_max_len":    120,
			"rate_per_sec":     7,
			"timeout":          "5s",
		},
		Quota:   config.QuotaConfig{PerSecond: 3, PerMinute: 60, PerHour: 600, PerDay: 6000, PerMonth: 60000},
		Editing: newChannel,
	}, cookie, true)
	if save.code != http.StatusOK {
		t.Fatalf("save: %d %s", save.code, save.text())
	}

	before, err := h.source.Get(context.Background(), "sink")
	if err != nil || before == nil {
		t.Fatalf("Get: %v", err)
	}

	form, _ := formFor(t, h, cookie, "sink")
	if form.EditType != "webhook" {
		t.Fatalf("edit_type = %q, want webhook", form.EditType)
	}

	// The body, built the way the client builds it.
	posted := map[string]any{}
	var present int
	for _, f := range form.Forms {
		if f.Type != form.EditType {
			continue
		}
		for _, field := range f.Fields {
			// A hidden field does not apply and is not sent. The hmac
			// parameters are behind auth_type, which is hmac, so they are.
			if field.Private {
				continue
			}
			switch field.Kind {
			case "checkbox":
				posted[field.Name] = field.Checked
				present++
			case "number":
				if field.Value == "" {
					continue
				}
				n, err := strconv.ParseFloat(field.Value, 64)
				if err != nil {
					t.Fatalf("%s renders %q, which is not a number", field.Name, field.Value)
				}
				posted[field.Name] = n
				present++
			case "list":
				if field.Value == "" {
					continue
				}
				var items []string
				for _, item := range strings.Split(field.Value, ",") {
					if item = strings.TrimSpace(item); item != "" {
						items = append(items, item)
					}
				}
				posted[field.Name] = items
				present++
			default:
				if field.Value == "" {
					continue
				}
				posted[field.Name] = field.Value
				present++
			}
		}
	}
	if present == 0 {
		t.Fatal("no field carried a rendered value; this test asserts nothing")
	}

	enabled := true
	again := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "sink", Type: "webhook", Enabled: &enabled,
		Config: posted, Quota: before.Quota, Editing: "sink",
	}, cookie, true)
	if again.code != http.StatusOK {
		t.Fatalf("saving the form's own values back was refused: %d %s\nposted: %v",
			again.code, again.text(), posted)
	}

	after, err := h.source.Get(context.Background(), "sink")
	if err != nil || after == nil {
		t.Fatalf("Get: %v", err)
	}

	// The credential survives, because a blank private field means "leave it
	// alone" — this is the other half of the same contract.
	if _, set := config.MaskSecrets(after.Type, after.Config); len(set) == 0 {
		t.Error("the stored credential did not survive the round trip")
	}

	for name, want := range before.Config {
		got := after.Config[name]
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: %v became %v; the form renders it in a way it cannot read back",
				name, want, got)
		}
	}
}

// A field the server does not know is refused rather than ignored. A typo'd
// quota that silently becomes "unlimited" is worse than an error message.
func TestSaveChannel_RefusesUnknownFields(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	res := h.do(t, http.MethodPost, "/admin/api/channels", map[string]any{
		"name": "sink", "type": "webhook",
		"config": map[string]any{"url": "https://example.com/notify", "auth_type": "none"},
		"quotas": map[string]any{"per_minute": 100},
	}, cookie, true)

	if res.code != http.StatusBadRequest {
		t.Errorf("status = %d for an unknown field, want 400: %s", res.code, res.text())
	}
}

// A configuration the channel refuses comes back with the complaints keyed by
// parameter name, so the form can put each one under the box it is about.
//
// The message goes out unchanged alongside them. It is what this endpoint has
// always returned, it is what a script reading it expects, and the map is an
// addition that a client ignoring it loses nothing from.
func TestSaveChannel_NamesTheFieldsItRefused(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	// Two declared parameters missing and one key the schema never declared.
	//
	// `to` used to be the third. It is optional now — a channel may take its
	// recipients from the request instead — so it is deliberately not in the
	// list below, and this comment is here so that its absence is not read as
	// an oversight.
	res := h.do(t, http.MethodPost, "/admin/api/channels", map[string]any{
		"name": "oncall", "type": "email",
		"config": map[string]any{"hots": "typo.example.com"},
	}, cookie, true)

	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.code, res.text())
	}

	var body struct {
		Error   string            `json:"error"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	}
	res.decode(t, &body)

	if body.Error != "invalid_config" {
		t.Errorf("error = %q, want invalid_config", body.Error)
	}
	if !strings.Contains(body.Message, "hots") {
		t.Errorf("message = %q, want the unknown key still named there", body.Message)
	}

	for _, name := range []string{"host", "from"} {
		got, ok := body.Fields[name]
		if !ok {
			t.Errorf("fields has no entry for %q: %v", name, body.Fields)
			continue
		}
		if !strings.Contains(got, "required") {
			t.Errorf("fields[%q] = %q", name, got)
		}
	}

	if _, ok := body.Fields["to"]; ok {
		t.Errorf("to is optional and must not be reported as refused: %v", body.Fields)
	}

	// The unknown key is not a field — nothing renders it, so a message keyed
	// to it would have nowhere to go. It stays in the message.
	if _, ok := body.Fields["hots"]; ok {
		t.Errorf("fields has an entry for the undeclared key: %v", body.Fields)
	}
}

// A refusal that belongs to no single field carries no map at all.
//
// A password with no username is the case: it is about two parameters and
// neither of them is wrong on its own. The form falls back to showing the
// message, which is what it did before any of this existed.
func TestSaveChannel_AnUnnamedRefusalCarriesNoFields(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	cfg := emailConfig("")
	cfg["password"] = "hunter2"
	cfg["host"] = "smtp.example.com"

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: cfg,
	}, cookie, true)

	if res.code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.code, res.text())
	}

	var body struct {
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	}
	res.decode(t, &body)

	if len(body.Fields) != 0 {
		t.Errorf("fields = %v, want none — the refusal names two parameters, not one", body.Fields)
	}
	if !strings.Contains(body.Message, "username") {
		t.Errorf("message = %q", body.Message)
	}
}

// A form opened to create a channel must not overwrite one that already has that
// name.
//
// The save endpoint is an upsert, which is right for a client posting a desired
// state and wrong for a form: the operator asked for a new channel, and what
// they got was a live one quietly replaced. So the form says what it meant, and
// the server answers the collision with a question instead of a write.
func TestSaveChannel_RefusesToOverwriteWhenTheFormMeantToCreate(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	replacement := saveRequest{
		Name: "oncall", Type: "webhook", Editing: newChannel,
		Config: map[string]any{"url": "https://example.com/notify", "auth_type": "none"},
	}

	res := h.do(t, http.MethodPost, "/admin/api/channels", replacement, cookie, true)
	if res.code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.code, res.text())
	}

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	res.decode(t, &body)
	if body.Error != "name_taken" {
		t.Errorf("error = %q, want name_taken — the form keys off this code, not off the message", body.Error)
	}
	if !strings.Contains(body.Message, "oncall") {
		t.Errorf("message = %q, want it to name what the collision is with", body.Message)
	}

	// Nothing changed. This is the assertion that matters: the refusal has to
	// happen before the write, not be reported after it.
	stored, err := h.source.Get(context.Background(), "oncall")
	if err != nil || stored == nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Type != "email" {
		t.Errorf("type = %q, want the channel that was delivering left alone", stored.Type)
	}
	if stored.Config["host"] != "smtp.example.com" {
		t.Errorf("the stored configuration was replaced: %+v", stored.Config)
	}

	// Confirming is what replaces it, and that is what the second submission is.
	replacement.Replace = true
	if res := h.do(t, http.MethodPost, "/admin/api/channels", replacement, cookie, true); res.code != http.StatusOK {
		t.Fatalf("confirmed replace: status %d: %s", res.code, res.text())
	}

	stored, err = h.source.Get(context.Background(), "oncall")
	if err != nil || stored == nil {
		t.Fatalf("Get after the replace: %v", err)
	}
	if stored.Type != "webhook" {
		t.Errorf("type = %q, want the replacement", stored.Type)
	}
}

// Editing a channel that exists is not a collision — that is the ordinary case,
// and the guard must not fire on it.
func TestSaveChannel_AnEditIsNotACollision(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	// The password is not sent back — the form was never shown it — so the
	// username has to stay for the merged configuration to be valid.
	edited := emailConfig("")
	edited["username"] = "notify@example.com"
	edited["host"] = "smtp2.example.com"

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Editing: "oncall", Config: edited,
	}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d editing a channel that exists, want 200: %s", res.code, res.text())
	}
}

// A caller that states no intent still gets the upsert this endpoint has always
// been. Every script using it posts a desired state, and refusing that would
// break them to protect a form they are not using.
func TestSaveChannel_AnUnstatedIntentStillUpserts(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "email", Config: emailConfig("hunter2"),
	}, cookie, true)

	res := h.do(t, http.MethodPost, "/admin/api/channels", saveRequest{
		Name: "oncall", Type: "webhook",
		Config: map[string]any{"url": "https://example.com/notify", "auth_type": "none"},
	}, cookie, true)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.code, res.text())
	}

	stored, err := h.source.Get(context.Background(), "oncall")
	if err != nil || stored == nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Type != "webhook" {
		t.Errorf("type = %q, want the posted state applied", stored.Type)
	}
}
