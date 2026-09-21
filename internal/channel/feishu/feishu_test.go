package feishu

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

type capture struct {
	mu       sync.Mutex
	requests [][]byte
	code     int
	msg      string
	status   int
}

func newCapture() *capture { return &capture{status: http.StatusOK, msg: "success"} }

func (c *capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	c.mu.Lock()
	c.requests = append(c.requests, body)
	code, msg, status := c.code, c.msg, c.status
	c.mu.Unlock()

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg})
}

func (c *capture) last(t *testing.T) []byte {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("Feishu received nothing")
	}
	return c.requests[len(c.requests)-1]
}

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
		Type:   message.TypeFailure,
		Links:  []message.Link{{Text: "Grafana", URL: "https://grafana.example.com"}},
	}
	m.Normalize()
	return m
}

// ---------------------------------------------------------------- signatures

// Feishu's convention is the mirror image of DingTalk's: the timestamp line is
// the HMAC KEY and the message is empty. Getting that backwards yields a
// signature that looks well-formed and is rejected by the platform.
func TestSign_UsesTheTimestampLineAsKeyAndAnEmptyMessage(t *testing.T) {
	const (
		secret = "SEC-test-secret"
		ts     = int64(1700000000)
	)

	want := func() string {
		stringToSign := strconv.FormatInt(ts, 10) + "\n" + secret
		mac := hmac.New(sha256.New, []byte(stringToSign))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}()

	if got := sign(secret, ts); got != want {
		t.Errorf("sign = %q, want %q", got, want)
	}

	// The DingTalk convention must not produce the same value, or this test
	// would pass with the roles swapped.
	dingtalk := func() string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(strconv.FormatInt(ts, 10) + "\n" + secret))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}()
	if dingtalk == want {
		t.Error("the Feishu and DingTalk conventions produced the same signature")
	}
}

func TestSign_ChangesWithInput(t *testing.T) {
	a := sign("a", 1700000000)
	if b := sign("b", 1700000000); a == b {
		t.Error("a different secret must produce a different signature")
	}
	if b := sign("a", 1700000001); a == b {
		t.Error("a different timestamp must produce a different signature")
	}
}

// ------------------------------------------------------------------- payload

func TestSend_CardPayload(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		MsgType string `json:"msg_type"`
		Card    struct {
			Header struct {
				Template string `json:"template"`
				Title    struct {
					Content string `json:"content"`
				} `json:"title"`
			} `json:"header"`
			Elements []struct {
				Text struct {
					Tag     string `json:"tag"`
					Content string `json:"content"`
				} `json:"text"`
			} `json:"elements"`
		} `json:"card"`
	}
	if err := json.Unmarshal(rec.last(t), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	if payload.MsgType != "interactive" {
		t.Errorf("msg_type = %q", payload.MsgType)
	}
	// The header colour is the only thing visible before the card is opened,
	// so it has to carry the severity.
	if payload.Card.Header.Template != "red" {
		t.Errorf("header template = %q, want red for a failure", payload.Card.Header.Template)
	}
	if !strings.Contains(payload.Card.Header.Title.Content, "数据库主从延迟告警") {
		t.Errorf("title = %q", payload.Card.Header.Title.Content)
	}
	if len(payload.Card.Elements) == 0 {
		t.Fatal("the card has no elements")
	}
	if payload.Card.Elements[0].Text.Tag != "lark_md" {
		t.Errorf("element tag = %q, want lark_md", payload.Card.Elements[0].Text.Tag)
	}
	if !strings.Contains(payload.Card.Elements[0].Text.Content, "https://grafana.example.com") {
		t.Errorf("links were dropped: %q", payload.Card.Elements[0].Text.Content)
	}
}

func TestSend_HeaderColourFollowsSeverity(t *testing.T) {
	tests := []struct {
		typ  message.Type
		want string
	}{
		{message.TypeInfo, "blue"},
		{message.TypeSuccess, "green"},
		{message.TypeWarning, "orange"},
		{message.TypeFailure, "red"},
	}

	for _, tt := range tests {
		t.Run(string(tt.typ), func(t *testing.T) {
			ch, rec := newChannel(t, func(url string) map[string]any {
				return map[string]any{"webhook_url": url}
			})

			msg := sampleMessage()
			msg.Type = tt.typ

			ch.Send(context.Background(), msg)

			var payload struct {
				Card struct {
					Header struct {
						Template string `json:"template"`
					} `json:"header"`
				} `json:"card"`
			}
			_ = json.Unmarshal(rec.last(t), &payload)

			if payload.Card.Header.Template != tt.want {
				t.Errorf("template = %q, want %q", payload.Card.Header.Template, tt.want)
			}
		})
	}
}

func TestSend_TextPayload(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url, "msg_type": "text"}
	})

	if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		MsgType string `json:"msg_type"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rec.last(t), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload.MsgType != "text" {
		t.Errorf("msg_type = %q", payload.MsgType)
	}
	if !strings.Contains(payload.Content.Text, "数据库主从延迟告警") {
		t.Errorf("text = %q", payload.Content.Text)
	}
}

func TestSend_SigningIsAppliedWhenConfigured(t *testing.T) {
	const secret = "my-secret"

	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url, "secret": secret}
	})

	if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassSent {
		t.Fatalf("class = %v (%v)", res.Class, res.Err)
	}

	var payload struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
	}
	if err := json.Unmarshal(rec.last(t), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}

	if payload.Timestamp != "1700000000" {
		t.Errorf("timestamp = %q, want the injected clock in seconds", payload.Timestamp)
	}
	if payload.Sign != sign(secret, 1700000000) {
		t.Errorf("sign = %q, want the expected signature", payload.Sign)
	}
}

func TestSend_NoSigningWithoutASecret(t *testing.T) {
	ch, rec := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	ch.Send(context.Background(), sampleMessage())

	var payload map[string]any
	_ = json.Unmarshal(rec.last(t), &payload)
	if _, ok := payload["sign"]; ok {
		t.Error("no secret should mean no signature")
	}
}

// ------------------------------------------------------------ classification

func TestSend_ClassifiesAPIErrors(t *testing.T) {
	tests := []struct {
		name string
		code int
		msg  string
		want channel.ResultClass
	}{
		{name: "ok", code: 0, msg: "success", want: channel.ClassSent},
		{name: "rate limited", code: 9499, msg: "Too Many Requests", want: channel.ClassTransient},
		{name: "frequency", code: 11232, msg: "frequency limit", want: channel.ClassTransient},
		{name: "bad signature", code: 19021, msg: "sign match fail", want: channel.ClassPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, rec := newChannel(t, func(url string) map[string]any {
				return map[string]any{"webhook_url": url}
			})
			rec.code, rec.msg = tt.code, tt.msg

			res := ch.Send(context.Background(), sampleMessage())
			if res.Class != tt.want {
				t.Errorf("class = %v, want %v (detail: %s)", res.Class, tt.want, res.Detail)
			}
		})
	}
}

// Feishu has used two response envelopes over time; a bot may answer with
// either, and both must be understood.
func TestSend_UnderstandsBothResponseEnvelopes(t *testing.T) {
	t.Run("StatusCode envelope", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"StatusCode": 19021, "StatusMessage": "sign match fail"})
		}))
		defer srv.Close()

		ch, err := New("test", map[string]any{"webhook_url": srv.URL})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())
		ch.(*Channel).client = httpx.NewClient(time.Second, &tls.Config{RootCAs: pool})

		if res := ch.Send(context.Background(), sampleMessage()); res.Class != channel.ClassPermanent {
			t.Errorf("class = %v, want PERMANENT (detail: %s)", res.Class, res.Detail)
		}
	})
}

func TestCapability(t *testing.T) {
	ch, _ := newChannel(t, func(url string) map[string]any {
		return map[string]any{"webhook_url": url}
	})

	cap := ch.Capability()
	if cap.MarkdownDialect != render.DialectFeishu {
		t.Errorf("dialect = %q, want feishu", cap.MarkdownDialect)
	}
	if cap.OverflowMode != channel.OverflowSplit {
		t.Errorf("overflow mode = %v, want split", cap.OverflowMode)
	}
	if cap.RatePerSec <= 0 {
		t.Error("Feishu rate-limits its bots; the channel must declare a rate")
	}
}

func TestNew_RejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]any
		wantSub string
	}{
		{name: "missing url", cfg: map[string]any{}, wantSub: "webhook_url"},
		{name: "non-https", cfg: map[string]any{"webhook_url": "http://open.feishu.cn/x"}, wantSub: "https"},
		{name: "bad msg_type", cfg: map[string]any{"webhook_url": "https://open.feishu.cn/x", "msg_type": "post"}, wantSub: "msg_type"},
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
