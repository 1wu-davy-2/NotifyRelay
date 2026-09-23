package dingtalk

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpx"
	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

// ---------------------------------------------------------------- test server

type capture struct {
	mu       sync.Mutex
	requests []recorded
	errcode  int
	errmsg   string
	status   int
}

type recorded struct {
	Query url.Values
	Body  []byte
}

func newCapture() *capture { return &capture{status: http.StatusOK, errmsg: "ok"} }

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	c.mu.Lock()
	c.requests = append(c.requests, recorded{Query: r.URL.Query(), Body: body})
	code, msg, status := c.errcode, c.errmsg, c.status
	c.mu.Unlock()

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errcode": code, "errmsg": msg})
}

func (c *capture) last(t *testing.T) recorded {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("DingTalk received nothing")
	}
	return c.requests[len(c.requests)-1]
}

func (c *capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// newChannel starts an HTTPS receiver (the channel requires https) and returns
// a channel pointed at it, trusting the test certificate through a real root
// pool rather than by disabling verification.
func newChannel(t *testing.T, configure func(url string) map[string]any) (*Channel, *capture) {
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
	channel.now = func() time.Time { return time.Unix(1700000000, 0) }

	return channel, rec
}

func sampleMessage() *message.Message {
	m := &message.Message{
		Title:  "数据库主从延迟告警",
		Body:   "**延迟**: 12s",
		Format: message.FormatMarkdown,
		Type:   message.TypeWarning,
		Links:  []message.Link{{Text: "Grafana", URL: "https://grafana.example.com"}},
	}
	m.Normalize()
	return m
}

// ---------------------------------------------------------------- signatures

// The test pins down which value is the HMAC key and which is the message.
// DingTalk and Feishu use opposite conventions, and swapping them produces a
// signature that looks perfectly well-formed and is rejected by the platform.
func TestSign_UsesTheSecretAsKeyAndTheTimestampLineAsMessage(t *testing.T) {
	const (
		secret = "SEC-test-secret"
		ts     = int64(1700000000000)
	)

	want := func() string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}()

	if got := sign(secret, ts); got != want {
		t.Errorf("sign = %q, want %q", got, want)
	}

	// The mirror-image algorithm must NOT produce the same value; if it does,
	// this test is not pinning anything down.
	mac := hmac.New(sha256.New, []byte(strconv.FormatInt(ts, 10)+"\n"+secret))
	mac.Write(nil)
	if mirrored := base64.StdEncoding.EncodeToString(mac.Sum(nil)); mirrored == want {
		t.Error("the DingTalk and Feishu conventions produced the same signature")
	}
}

func TestSign_ChangesWithInput(t *testing.T) {
	a := sign("secret-a", 1700000000000)
	if b := sign("secret-b", 1700000000000); a == b {
		t.Error("a different secret must produce a different signature")
	}
	if b := sign("secret-a", 1700000000001); a == b {
		t.Error("a different timestamp must produce a different signature")
	}
}

func TestSignedURL(t *testing.T) {
	const base = "https://oapi.dingtalk.com/robot/send?access_token=abc"

	t.Run("adds timestamp and sign", func(t *testing.T) {
		const ts = int64(1700000000000)

		got, err := signedURL(base, "sec", ts)
		if err != nil {
			t.Fatalf("signedURL: %v", err)
		}

		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatalf("the signed URL is not parseable: %v", err)
		}
		query := parsed.Query()

		if query.Get("access_token") != "abc" {
			t.Error("signing dropped the access token")
		}
		if query.Get("timestamp") != strconv.FormatInt(ts, 10) {
			t.Errorf("timestamp = %q", query.Get("timestamp"))
		}
		if query.Get("sign") != sign("sec", ts) {
			t.Error("the sign parameter does not match the signature")
		}
	})

	t.Run("no secret leaves the URL alone", func(t *testing.T) {
		got, err := signedURL(base, "", 1700000000000)
		if err != nil {
			t.Fatalf("signedURL: %v", err)
		}
		if got != base {
			t.Errorf("URL = %q, want it unchanged", got)
		}
	})

	// A base64 signature contains '+' and '/', which must survive the query
	// round trip rather than being read as a space.
	t.Run("signature survives escaping", func(t *testing.T) {
		got, err := signedURL(base, "secret with + and /", 1700000000123)
		if err != nil {
			t.Fatalf("signedURL: %v", err)
		}
		parsed, _ := url.Parse(got)

		if parsed.Query().Get("sign") != sign("secret with + and /", 1700000000123) {
			t.Error("the signature was mangled by query escaping")
		}
	})
}

// ------------------------------------------------------------------ payloads

func TestSend_MarkdownPayload(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url, "at_mobiles": []any{"13800000000"}}
	})

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		MsgType  string `json:"msgtype"`
		Markdown struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"markdown"`
		At struct {
			AtMobiles []string `json:"atMobiles"`
		} `json:"at"`
	}
	if err := json.Unmarshal(rec.last(t).Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	if payload.MsgType != "markdown" {
		t.Errorf("msgtype = %q", payload.MsgType)
	}
	if !strings.Contains(payload.Markdown.Title, "数据库主从延迟告警") {
		t.Errorf("title = %q", payload.Markdown.Title)
	}
	if !strings.HasPrefix(payload.Markdown.Title, "[WARN]") {
		t.Errorf("the title should carry the severity, got %q", payload.Markdown.Title)
	}
	if !strings.Contains(payload.Markdown.Text, "https://grafana.example.com") {
		t.Errorf("links were dropped: %q", payload.Markdown.Text)
	}
	if len(payload.At.AtMobiles) != 1 {
		t.Errorf("atMobiles = %v", payload.At.AtMobiles)
	}
}

func TestSend_TextPayload(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url, "msg_type": "text"}
	})

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.last(t).Body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload["msgtype"] != "text" {
		t.Errorf("msgtype = %v", payload["msgtype"])
	}
	if _, ok := payload["markdown"]; ok {
		t.Error("a text message must not carry a markdown field")
	}
}

func TestSend_AtAll(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url, "at_all": true}
	})

	ch.Send(context.Background(), sampleMessage(), channel.Target{})

	var payload struct {
		At struct {
			IsAtAll bool `json:"isAtAll"`
		} `json:"at"`
	}
	_ = json.Unmarshal(rec.last(t).Body, &payload)
	if !payload.At.IsAtAll {
		t.Error("at_all should set isAtAll in the payload")
	}
}

func TestSend_SigningIsAppliedWhenConfigured(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url + "?access_token=abc", "secret": "my-secret"}
	})

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	query := rec.last(t).Query
	if query.Get("sign") == "" || query.Get("timestamp") == "" {
		t.Fatal("a configured secret must produce signature parameters")
	}
	if query.Get("access_token") != "abc" {
		t.Error("signing dropped the access token")
	}
}

func TestSend_NoSigningWithoutASecret(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	if got := rec.last(t).Query.Get("sign"); got != "" {
		t.Errorf("no secret should mean no signature, got sign=%q", got)
	}
}

// ------------------------------------------------------------ classification

// DingTalk answers HTTP 200 with a non-zero errcode for application errors, so
// the transport result alone cannot be trusted.
func TestSend_ClassifiesAPIErrors(t *testing.T) {
	tests := []struct {
		name    string
		errcode int
		errmsg  string
		want    channel.ResultClass
	}{
		{name: "ok", errcode: 0, errmsg: "ok", want: channel.ClassSent},
		{name: "send too fast", errcode: 130101, errmsg: "send too fast", want: channel.ClassTransient},
		{name: "frequent", errcode: 410100, errmsg: "请求过于频繁", want: channel.ClassTransient},
		{name: "server fault", errcode: -1, errmsg: "system error", want: channel.ClassTransient},
		{name: "bad token", errcode: 300001, errmsg: "invalid access_token", want: channel.ClassPermanent},
		{name: "keyword blocked", errcode: 310000, errmsg: "message contains a blocked keyword", want: channel.ClassPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, rec := newChannel(t, func(url string) map[string]any {
				return map[string]any{"webhook_url": url}
			})
			rec.errcode, rec.errmsg = tt.errcode, tt.errmsg

			res := ch.Send(context.Background(), sampleMessage(), channel.Target{})
			if res.Class != tt.want {
				t.Errorf("class = %v, want %v (detail: %s)", res.Class, tt.want, res.Detail)
			}
		})
	}
}

func TestSend_HTTPFailureIsClassified(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})
	rec.status = http.StatusInternalServerError

	if res := ch.Send(context.Background(), sampleMessage(), channel.Target{}); res.Class != channel.ClassTransient {
		t.Errorf("class = %v, want TRANSIENT for a 500", res.Class)
	}
}

// ------------------------------------------------------------------ capability

func TestCapability(t *testing.T) {
	ch, _ := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	cap := ch.Capability()

	if cap.MarkdownDialect != render.DialectDingTalk {
		t.Errorf("dialect = %q, want dingtalk", cap.MarkdownDialect)
	}
	// The byte cap is the real platform limit and must be enforced; a rune
	// count alone cannot express a 20000-byte ceiling.
	if cap.BodyMaxBytes != defaultBodyMaxBytes {
		t.Errorf("body byte limit = %d, want %d", cap.BodyMaxBytes, defaultBodyMaxBytes)
	}
	if cap.OverflowMode != channel.OverflowSplit {
		t.Errorf("overflow mode = %v, want split", cap.OverflowMode)
	}
	// DingTalk mutes a robot for ten minutes when its rate is exceeded, so the
	// declared rate must be the documented 20 per minute, not something faster.
	if want := 20.0 / 60.0; cap.RatePerSec != want {
		t.Errorf("rate = %v, want %v", cap.RatePerSec, want)
	}
}

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantSub string
	}{
		{name: "missing url", cfg: map[string]any{}, wantSub: "webhook_url"},
		{name: "non-https", cfg: map[string]any{"webhook_url": "http://oapi.dingtalk.com/x"}, wantSub: "https"},
		{name: "bad msg_type", cfg: map[string]any{"webhook_url": "https://oapi.dingtalk.com/x", "msg_type": "card"}, wantSub: "msg_type"},
		{name: "negative rate", cfg: map[string]any{"webhook_url": "https://oapi.dingtalk.com/x", "rate_per_sec": -1}, wantSub: "at least 0"},
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
