package slack

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
	"testing"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

type capture struct {
	mu       sync.Mutex
	requests []recorded
	status   int
	reply    string
}

type recorded struct {
	Path          string
	Authorization string
	Body          []byte
}

func newCapture() *capture { return &capture{status: http.StatusOK, reply: "ok"} }

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	c.mu.Lock()
	c.requests = append(c.requests, recorded{
		Path:          r.URL.Path,
		Authorization: r.Header.Get("Authorization"),
		Body:          body,
	})
	status, reply := c.status, c.reply
	c.mu.Unlock()

	w.WriteHeader(status)
	_, _ = w.Write([]byte(reply))
}

func (c *capture) last(t *testing.T) recorded {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("Slack received nothing")
	}
	return c.requests[len(c.requests)-1]
}

// newTLSChannel starts an HTTPS receiver and returns a channel pointed at it.
//
// Slack endpoints are https-only and the channel enforces that, so the test
// server has to speak TLS too. Its certificate is trusted through a real root
// pool rather than by disabling verification.
func newTLSChannel(t *testing.T, configure func(url string) map[string]any) (*Channel, *capture, string) {
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
	channel.client = httpx.NewClient(time.Second, &tls.Config{RootCAs: pool})

	return channel, rec, srv.URL
}

func sampleMessage() *message.Message {
	m := &message.Message{
		Title:  "数据库主从延迟告警",
		Body:   "延迟 12s",
		Format: message.FormatText,
		Type:   message.TypeWarning,
		Links:  []message.Link{{Text: "Grafana", URL: "https://grafana.example.com"}},
	}
	m.Normalize()
	return m
}

func TestSend_IncomingWebhook(t *testing.T) {
	ch, rec, _ := newTLSChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	res := ch.Send(context.Background(), sampleMessage(), channel.Target{})
	if res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.last(t).Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	text := payload["text"]
	for _, want := range []string{"[WARN]", "数据库主从延迟告警", "延迟 12s", "https://grafana.example.com"} {
		if !strings.Contains(text, want) {
			t.Errorf("text is missing %q:\n%s", want, text)
		}
	}
	if _, ok := payload["channel"]; ok {
		t.Error("an incoming webhook payload must not carry a channel field")
	}
}

func TestSend_BotToken(t *testing.T) {
	ch, rec, base := newTLSChannel(t, func(string) map[string]any {
		return map[string]any{"token": "xoxb-secret", "channel": "#alerts"}
	})
	// The API endpoints are fields rather than constants so the request path
	// can be exercised; a hardcoded host would make it untestable.
	ch.postURL = base + "/api/chat.postMessage"

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	got := rec.last(t)
	if got.Authorization != "Bearer xoxb-secret" {
		t.Errorf("Authorization = %q", got.Authorization)
	}

	var payload map[string]string
	if err := json.Unmarshal(got.Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	// A leading '#' is how a human writes a channel; the API wants the name.
	if payload["channel"] != "alerts" {
		t.Errorf("channel = %q, want alerts", payload["channel"])
	}
}

// chat.postMessage reports application errors inside a 200 response, so a 200
// on its own does not mean the message arrived.
func TestSend_BotTokenAPIErrorIsAFailure(t *testing.T) {
	tests := []struct {
		name     string
		reply    string
		want     channel.ResultClass
		wantText string
	}{
		{
			name:     "channel not found",
			reply:    `{"ok":false,"error":"channel_not_found"}`,
			want:     channel.ClassPermanent,
			wantText: "channel_not_found",
		},
		{
			name:     "invalid token",
			reply:    `{"ok":false,"error":"invalid_auth"}`,
			want:     channel.ClassPermanent,
			wantText: "invalid_auth",
		},
		{
			// Being rate limited is the one API error a retry can fix.
			name:     "rate limited",
			reply:    `{"ok":false,"error":"ratelimited"}`,
			want:     channel.ClassTransient,
			wantText: "ratelimited",
		},
		{
			name:  "ok",
			reply: `{"ok":true,"ts":"1.2"}`,
			want:  channel.ClassSent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, rec, base := newTLSChannel(t, func(string) map[string]any {
				return map[string]any{"token": "xoxb-secret", "channel": "alerts"}
			})
			ch.postURL = base + "/api/chat.postMessage"
			rec.reply = tt.reply

			res := ch.Send(context.Background(), sampleMessage(), channel.Target{})
			if res.Class != tt.want {
				t.Errorf("class = %v, want %v (detail: %s)", res.Class, tt.want, res.Detail)
			}
			if tt.wantText != "" && !strings.Contains(res.Detail, tt.wantText) {
				t.Errorf("detail %q should mention %q", res.Detail, tt.wantText)
			}
		})
	}
}

func TestSend_HTTPFailureIsClassified(t *testing.T) {
	ch, rec, _ := newTLSChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})
	rec.status = http.StatusInternalServerError

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassTransient {
		t.Errorf("class = %v, want TRANSIENT for a 500", res.Class)
	}
}

// Slack has no separate title field, so the title leads the text with the
// severity marker mail puts in the subject line.
func TestRenderText_Severity(t *testing.T) {
	tests := []struct {
		typ  message.Type
		want string
	}{
		{message.TypeInfo, ""},
		{message.TypeSuccess, "[OK] "},
		{message.TypeWarning, "[WARN] "},
		{message.TypeFailure, "[ALERT] "},
	}

	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			m := &message.Message{Title: "t", Body: "b", Type: tt.typ}
			m.Normalize()

			got := renderText(m)
			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("text = %q, want prefix %q", got, tt.want)
			}
		})
	}
}

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantSub string
	}{
		{name: "neither mode", cfg: map[string]any{}, wantSub: "required"},
		{
			name:    "both modes",
			cfg:     map[string]any{"webhook_url": "https://hooks.slack.com/x", "token": "t", "channel": "c"},
			wantSub: "not both",
		},
		{name: "token without channel", cfg: map[string]any{"token": "t"}, wantSub: "channel"},
		{name: "non-https webhook", cfg: map[string]any{"webhook_url": "http://hooks.slack.com/x"}, wantSub: "https"},
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

func TestCapability(t *testing.T) {
	ch, err := New("test", map[string]any{"webhook_url": "https://hooks.slack.com/services/x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cap := ch.Capability()
	if !cap.Supports(message.FormatMarkdown) {
		t.Errorf("supported formats = %v, want markdown among them", cap.SupportedFormats)
	}
	// The dialect is what makes markdown safe to send: without it, CommonMark
	// bold would reach readers as literal double asterisks.
	if cap.MarkdownDialect != render.DialectSlack {
		t.Errorf("markdown dialect = %q, want slack", cap.MarkdownDialect)
	}
	if cap.BodyMaxLen != defaultBodyMaxLen {
		t.Errorf("body limit = %d, want %d", cap.BodyMaxLen, defaultBodyMaxLen)
	}
	if cap.OverflowMode != channel.OverflowSplit {
		t.Errorf("overflow mode = %v, want split", cap.OverflowMode)
	}
	if cap.RatePerSec <= 0 {
		t.Error("Slack rate-limits its API; the channel must declare a rate")
	}
}
