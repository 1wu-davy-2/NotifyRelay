package email

import (
	"bytes"
	"context"
	"io"
	mimepkg "mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-smtp"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

// ---------------------------------------------------------------- fake server

type capturedMessage struct {
	From string
	To   []string
	Data []byte
}

// fakeBackend is an in-process SMTP server that records what it receives and
// can be told to reject specific recipients.
type fakeBackend struct {
	mu       sync.Mutex
	messages []capturedMessage
	reject   map[string]error
	// rejectFrom, when set, refuses every MAIL FROM — the shape of a relay that
	// turns the sender away before it has looked at any recipient. Set before
	// the server starts, so it needs no lock.
	rejectFrom error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{reject: map[string]error{}}
}

func (b *fakeBackend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &fakeSession{backend: b}, nil
}

func (b *fakeBackend) record(m capturedMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = append(b.messages, m)
}

func (b *fakeBackend) captured() []capturedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]capturedMessage(nil), b.messages...)
}

type fakeSession struct {
	backend *fakeBackend
	from    string
	rcpts   []string
}

func (s *fakeSession) Reset()         { s.from = ""; s.rcpts = nil }
func (s *fakeSession) Logout() error  { return nil }
func (s *fakeSession) Mail(from string, _ *smtp.MailOptions) error {
	if s.backend.rejectFrom != nil {
		return s.backend.rejectFrom
	}
	s.from = from
	return nil
}

func (s *fakeSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if err, ok := s.backend.reject[to]; ok {
		return err
	}
	s.rcpts = append(s.rcpts, to)
	return nil
}

func (s *fakeSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.backend.record(capturedMessage{From: s.from, To: s.rcpts, Data: data})
	return nil
}

// startFakeSMTP listens on an ephemeral port and returns host and port.
func startFakeSMTP(t *testing.T, b *fakeBackend) (string, int) {
	t.Helper()

	srv := smtp.NewServer(b)
	srv.Domain = "test.local"
	srv.AllowInsecureAuth = true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func newTestChannel(t *testing.T, cfg map[string]any) *Channel {
	t.Helper()
	ch, err := New("test", cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ec, ok := ch.(*Channel)
	if !ok {
		t.Fatalf("New returned %T, want *Channel", ch)
	}
	return ec
}

func plainMessage() *message.Message {
	m := &message.Message{
		Title:  "数据库主从延迟告警",
		Body:   "延迟 12s，实例 db-03",
		Format: message.FormatText,
		Type:   message.TypeWarning,
	}
	m.Normalize()
	return m
}

// ------------------------------------------------------------------- sending

func TestSend_ReachesTheServer(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassSent {
		t.Fatalf("Send class = %v (%v), want SENT", res.Class, res.Err)
	}

	got := be.captured()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	if got[0].From != "relay@example.com" {
		t.Errorf("MAIL FROM = %q, want relay@example.com", got[0].From)
	}
	if len(got[0].To) != 1 || got[0].To[0] != "ops@example.com" {
		t.Errorf("RCPT TO = %v, want [ops@example.com]", got[0].To)
	}
}

// A Chinese subject that arrives as mojibake is the single most visible
// failure this channel can have, so it is asserted byte for byte.
func TestSend_ChineseSubjectSurvivesTheWire(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com"},
	})

	if res := ch.Send(context.Background(), plainMessage(), channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("Send class = %v (%v)", res.Class, res.Err)
	}

	raw := be.captured()[0].Data
	subject := rawSubject(t, raw)

	const want = "[WARN] 数据库主从延迟告警"
	if subject != want {
		t.Errorf("subject decoded to %q, want %q", subject, want)
	}

	// Non-ASCII headers must be RFC 2047 encoded rather than left as raw
	// UTF-8: not every receiving MTA speaks SMTPUTF8.
	if bytes.Contains(raw, []byte("数据库主从延迟告警")) {
		t.Error("subject was transmitted as raw UTF-8; it must be RFC 2047 encoded")
	}
}

func TestSend_HTMLBodyGetsAPlainTextAlternative(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com"},
	})

	msg := &message.Message{
		Title:  "deploy",
		Body:   "<p>Deploy <strong>finished</strong></p>",
		Format: message.FormatHTML,
		Type:   message.TypeSuccess,
	}
	msg.Normalize()

	if res := ch.Send(context.Background(), msg, channel.Target{}); res.Class != channel.ClassSent {
		t.Fatalf("Send class = %v (%v)", res.Class, res.Err)
	}

	raw := string(be.captured()[0].Data)
	if !strings.Contains(raw, "multipart/alternative") {
		t.Fatalf("body is not multipart/alternative:\n%s", raw)
	}
	if !strings.Contains(raw, "text/plain") || !strings.Contains(raw, "text/html") {
		t.Errorf("both parts must be present:\n%s", raw)
	}
	if !strings.Contains(raw, "quoted-printable") {
		t.Errorf("body must be quoted-printable encoded:\n%s", raw)
	}

	parts := decodeParts(t, []byte(raw))
	if !strings.Contains(parts["text/plain"], "Deploy finished") {
		t.Errorf("plain-text alternative lost the content: %q", parts["text/plain"])
	}
	if !strings.Contains(parts["text/html"], "<strong>finished</strong>") {
		t.Errorf("html part lost its markup: %q", parts["text/html"])
	}
}

// --------------------------------------------------------------- recipients

// Each recipient is delivered independently, so one bad address must not
// obscure the others.
func TestSend_PartialRecipientFailureIsReportedPerRecipient(t *testing.T) {
	be := newFakeBackend()
	be.reject["gone@example.com"] = &smtp.SMTPError{Code: 550, Message: "mailbox unavailable"}
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com", "gone@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})

	if len(res.Recipients) != 2 {
		t.Fatalf("got %d recipient results, want 2", len(res.Recipients))
	}

	byAddr := map[string]channel.Recipient{}
	for _, r := range res.Recipients {
		byAddr[r.Address] = r
	}

	if r := byAddr["ops@example.com"]; !r.Accepted || r.Class != channel.ClassSent {
		t.Errorf("ops@example.com = %+v, want accepted", r)
	}
	if r := byAddr["gone@example.com"]; r.Accepted || r.Class != channel.ClassPermanent {
		t.Errorf("gone@example.com = %+v, want permanent rejection", r)
	}

	// One recipient succeeded, so a blanket "failed" would be wrong, but the
	// permanent rejection must not be reported as success either.
	if res.Class == channel.ClassSent {
		t.Error("overall class is SENT despite a rejected recipient")
	}
}

// ------------------------------------------------------------ classification

func TestClassify_TemporaryRejectionIsTransient(t *testing.T) {
	be := newFakeBackend()
	be.reject["busy@example.com"] = &smtp.SMTPError{Code: 450, Message: "try again later"}
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"busy@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassTransient {
		t.Fatalf("class = %v (%v), want TRANSIENT", res.Class, res.Err)
	}
	if !res.Class.Retryable() {
		t.Error("TRANSIENT must be retryable")
	}
}

func TestClassify_PermanentRejectionIsPermanent(t *testing.T) {
	be := newFakeBackend()
	be.reject["gone@example.com"] = &smtp.SMTPError{Code: 550, Message: "no such user"}
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"gone@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassPermanent {
		t.Fatalf("class = %v (%v), want PERMANENT", res.Class, res.Err)
	}
	if res.Class.Retryable() {
		t.Error("PERMANENT must not be retryable")
	}
}

// The sentence the server wrote has to survive into the detail.
//
// It is the difference between a record that says a delivery failed and one
// that says why. A relay refusing the sender, a relay refusing the recipient
// and a relay that is simply unreachable all arrive as a class and a code, and
// the reply text is the only part an operator can act on. It used to be dropped
// twice over: classify kept go-mail's name for the step that failed, and
// FromRecipients did not summarise the recipients at all — so the attempts
// table showed "no channel capacity" for a relay that had answered in plain
// words.
func TestClassify_KeepsTheServersOwnWords(t *testing.T) {
	const reply = "mail from address must be same as authorization user"

	be := newFakeBackend()
	be.rejectFrom = &smtp.SMTPError{Code: 501, Message: reply}
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "noreply@example.com",
		"to":   []any{"ops@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})

	if res.Class != channel.ClassPermanent {
		t.Fatalf("class = %v (%v), want PERMANENT for a 501", res.Class, res.Err)
	}
	if len(res.Recipients) != 1 {
		t.Fatalf("got %d recipient results, want 1", len(res.Recipients))
	}
	if !strings.Contains(res.Recipients[0].Detail, reply) {
		t.Errorf("recipient detail = %q, want the server's reply in it", res.Recipients[0].Detail)
	}
	// And it has to reach the overall result, which is the only thing the queue
	// records.
	if !strings.Contains(res.Detail, reply) {
		t.Errorf("overall detail = %q, want the server's reply carried up from the recipient", res.Detail)
	}
}

// A refused connection must be CONNECT_ERROR, not TRANSIENT: the message never
// touched the peer, so it must not consume the channel's send quota.
func TestClassify_UnreachableServerIsConnectError(t *testing.T) {
	// Bind and immediately release a port so nothing is listening on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()

	port, _ := strconv.Atoi(portStr)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com"},
		"timeout": "2s",
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{})
	if res.Class != channel.ClassConnectError {
		t.Fatalf("class = %v (%v), want CONNECT_ERROR", res.Class, res.Err)
	}
	if !res.Class.Retryable() {
		t.Error("CONNECT_ERROR must be retryable")
	}
}

// ---------------------------------------------------------------- test helper

func TestTest_ReportsConnectivity(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"ops@example.com"},
	})

	if res := ch.Test(context.Background()); res.Class != channel.ClassSent {
		t.Errorf("Test on a live server = %v (%v), want SENT", res.Class, res.Err)
	}
}

// ------------------------------------------------------------------ decoding

func rawSubject(t *testing.T, raw []byte) string {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	decoded, err := new(mimepkg.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode subject %q: %v", msg.Header.Get("Subject"), err)
	}
	return decoded
}

// decodeParts returns the decoded body of each text part by content type.
func decodeParts(t *testing.T, raw []byte) map[string]string {
	t.Helper()

	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}

	mediaType, params, err := mimepkg.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("content type = %q, want multipart/*", mediaType)
	}

	out := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		pt, _, _ := mimepkg.ParseMediaType(part.Header.Get("Content-Type"))
		var body []byte
		if part.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
			body, _ = io.ReadAll(quotedprintable.NewReader(part))
		} else {
			body, _ = io.ReadAll(part)
		}
		out[pt] = string(body)
	}
	return out
}
