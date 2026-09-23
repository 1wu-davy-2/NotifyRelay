package wecom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

type capture struct {
	mu       sync.Mutex
	requests [][]byte
	errcode  int
	errmsg   string
	// tokenCalls counts requests to the gettoken endpoint.
	tokenCalls int32
	tokenReply string
	tokenTTL   int
	status     int
}

func newCapture() *capture {
	return &capture{status: http.StatusOK, errmsg: "ok", tokenReply: "ACCESS-TOKEN", tokenTTL: 7200}
}

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	code, msg, status := c.errcode, c.errmsg, c.status

	if strings.Contains(r.URL.Path, "gettoken") {
		atomic.AddInt32(&c.tokenCalls, 1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errcode": 0, "errmsg": "ok",
			"access_token": c.tokenReply, "expires_in": c.tokenTTL,
		})
		return
	}

	c.mu.Lock()
	c.requests = append(c.requests, body)
	c.mu.Unlock()

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errcode": code, "errmsg": msg})
}

func (c *capture) last(t *testing.T) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("WeCom received nothing")
	}
	return c.requests[len(c.requests)-1]
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *capture) tokens() int { return int(atomic.LoadInt32(&c.tokenCalls)) }

// newChannel starts an HTTPS receiver and returns a channel whose API endpoints
// point at it. The URLs are fields rather than constants precisely so that the
// application-mode request path can be exercised.
func newChannel(t *testing.T, configure func(base string) map[string]any) (*Channel, *capture) {
	t.Helper()

	rec := newCapture()
	srv := httptest.NewTLSServer(rec)
	t.Cleanup(srv.Close)

	ch, err := New("test", configure(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	channel := ch.(*Channel)
	channel.client = httpx.NewClient(5*time.Second, &tls.Config{RootCAs: pool})
	channel.getTokenURL = srv.URL + "/cgi-bin/gettoken"
	channel.sendURL = srv.URL + "/cgi-bin/message/send"

	return channel, rec
}

func sampleMessage() *message.Message {
	m := &message.Message{
		Title:  "数据库主从延迟告警",
		Body:   "**延迟**: 12s",
		Format: message.FormatMarkdown,
		Type:   message.TypeWarning,
	}
	m.Normalize()
	return m
}

func appConfig(base string) map[string]any {
	return map[string]any{
		"mode": "app", "corp_id": "corp", "corp_secret": "secret",
		"agent_id": 1000002, "to_user": "@all",
	}
}

// ------------------------------------------------------------------ webhook

func TestSend_WebhookPayload(t *testing.T) {
	ch, rec := newChannel(t, func(base string) map[string]any {
		return map[string]any{"mode": "webhook", "webhook_url": base + "/cgi-bin/webhook/send?key=abc"}
	})

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		MsgType  string `json:"msgtype"`
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal(rec.last(t), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	if payload.MsgType != "markdown" {
		t.Errorf("msgtype = %q", payload.MsgType)
	}
	if !strings.Contains(payload.Markdown.Content, "数据库主从延迟告警") {
		t.Errorf("content = %q", payload.Markdown.Content)
	}
	// WeCom has no red in its palette; a failure gets the most alarming colour
	// that exists rather than one that does not.
	if !strings.Contains(payload.Markdown.Content, "<font color=\"warning\">") {
		t.Errorf("severity colour is missing: %q", payload.Markdown.Content)
	}
	if rec.tokens() != 0 {
		t.Error("webhook mode must not fetch an access token")
	}
}

// ---------------------------------------------------------------- app mode

func TestSend_AppModeUsesTheTokenAndAddressesTheRecipient(t *testing.T) {
	ch, rec := newChannel(t, appConfig)

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		ToUser  string `json:"touser"`
		MsgType string `json:"msgtype"`
		AgentID int    `json:"agentid"`
	}
	if err := json.Unmarshal(rec.last(t), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	if payload.ToUser != "@all" {
		t.Errorf("touser = %q", payload.ToUser)
	}
	if payload.AgentID != 1000002 {
		t.Errorf("agentid = %d", payload.AgentID)
	}
	if rec.tokens() != 1 {
		t.Errorf("fetched %d tokens, want 1", rec.tokens())
	}
}

// The acceptance criterion: twenty concurrent sends must not cause twenty
// credential requests. The platform would see each one, and nineteen tokens
// would be thrown away.
func TestSend_ConcurrentSendsFetchOneToken(t *testing.T) {
	ch, rec := newChannel(t, appConfig)

	const goroutines = 20
	var wg sync.WaitGroup
	results := make([]channel.Result, goroutines)

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = ch.Send(context.Background(), sampleMessage(), channel.Target{})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, res := range results {
		if res.Class != channel.ClassSent {
			t.Errorf("send %d: class = %v (%v)", i, res.Class, res.Err)
		}
	}
	if got := rec.tokens(); got != 1 {
		t.Errorf("%d concurrent sends fetched %d tokens, want 1", goroutines, got)
	}
	if got := rec.count(); got != goroutines {
		t.Errorf("%d messages reached the endpoint, want %d", got, goroutines)
	}
}

func TestSend_TokenIsReusedAcrossSends(t *testing.T) {
	ch, rec := newChannel(t, appConfig)

	for i := 0; i < 5; i++ {
		if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
			t.Fatalf("send %d: %v", i, res.Err)
		}
	}

	if got := rec.tokens(); got != 1 {
		t.Errorf("fetched %d tokens for 5 sends, want 1", got)
	}
}

// A token the platform no longer accepts is the normal lifecycle of a WeCom
// credential, not an error: the process refreshes and carries on.
func TestSend_ExpiredTokenIsRefreshedAndRetried(t *testing.T) {
	ch, rec := newChannel(t, appConfig)

	// While the server rejects the token, the send is retried once with a
	// refreshed token and then reported as the server described it.
	rec.errcode, rec.errmsg = errCodeInvalidToken, "invalid credential"

	res := ch.Send(context.Background(), sampleMessage(), channel.Target{})
	if res.Class != channel.ClassTransient {
		t.Fatalf("class = %v, want TRANSIENT while the server keeps refusing", res.Class)
	}
	if got := rec.tokens(); got < 2 {
		t.Errorf("a rejected token should have been refreshed, got %d token fetches", got)
	}

	// Let the server accept. The channel must recover with no intervention.
	rec.errcode, rec.errmsg = 0, "ok"
	before := rec.tokens()

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Errorf("class = %v (%v), want SENT", res.Class, res.Err)
	}
	if got := rec.tokens(); got != before {
		t.Errorf("a working token should have been reused, got %d fetches (was %d)", got, before)
	}
}

func TestSend_RateLimitIsTransient(t *testing.T) {
	ch, rec := newChannel(t, appConfig)
	rec.errcode, rec.errmsg = 45009, "api freq out of limit"

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassTransient {
		t.Errorf("class = %v, want TRANSIENT", res.Class)
	}
}

func TestSend_PermanentErrorIsNotRetried(t *testing.T) {
	ch, rec := newChannel(t, appConfig)
	rec.errcode, rec.errmsg = 81013, "user not found"

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassPermanent {
		t.Errorf("class = %v, want PERMANENT", res.Class)
	}
	// A rejected token triggers a refresh and one retry; a rejected recipient
	// must not.
	if got := rec.count(); got != 1 {
		t.Errorf("the message was posted %d times, want 1", got)
	}
}

// An unreachable token endpoint means no request reached the message endpoint,
// so nothing was rejected: this is a connection failure, not a bad message.
func TestSend_TokenFetchFailureIsConnectError(t *testing.T) {
	ch, _ := newChannel(t, appConfig)

	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	ch.getTokenURL = url + "/cgi-bin/gettoken"

	res := ch.Send(context.Background(), sampleMessage(), channel.Target{})
	if res.Class != channel.ClassConnectError {
		t.Errorf("class = %v (%s), want CONNECT_ERROR", res.Class, res.Detail)
	}
}

// ------------------------------------------------------------------ capability

func TestCapability(t *testing.T) {
	ch, _ := newChannel(t, func(base string) map[string]any {
		return map[string]any{"mode": "webhook", "webhook_url": base + "/x?key=abc"}
	})

	cap := ch.Capability()

	if cap.MarkdownDialect != render.DialectWeCom {
		t.Errorf("dialect = %q, want wecom", cap.MarkdownDialect)
	}
	// WeCom caps a markdown message at 4096 BYTES, which is the limit that
	// actually rejects a message; the rune count cannot express it.
	if cap.BodyMaxBytes != defaultBodyMaxBytes {
		t.Errorf("byte limit = %d, want %d", cap.BodyMaxBytes, defaultBodyMaxBytes)
	}
	if cap.OverflowMode != channel.OverflowSplit {
		t.Errorf("overflow mode = %v, want split", cap.OverflowMode)
	}
}

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantSub string
	}{
		{name: "webhook without a url", cfg: map[string]any{"mode": "webhook"}, wantSub: "webhook_url"},
		{name: "webhook with a non-https url", cfg: map[string]any{"mode": "webhook", "webhook_url": "http://qyapi.weixin.qq.com/x"}, wantSub: "https"},
		{name: "app without credentials", cfg: map[string]any{"mode": "app"}, wantSub: "corp_id"},
		{name: "app without an agent id", cfg: map[string]any{"mode": "app", "corp_id": "c", "corp_secret": "s", "to_user": "@all"}, wantSub: "agent_id"},
		{name: "app without a recipient", cfg: map[string]any{"mode": "app", "corp_id": "c", "corp_secret": "s", "agent_id": 1}, wantSub: "to_user"},
		{name: "unknown mode", cfg: map[string]any{"mode": "robot"}, wantSub: "mode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("test", tt.cfg)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error %q should mention %q", err, tt.wantSub)
			}
		})
	}
}
