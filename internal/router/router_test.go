package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
)

// ---------------------------------------------------------------- fake channel

var registerOnce sync.Once

type fakeChannel struct {
	typeName     string
	bodyMaxLen   int
	formats      []message.Format
	overflowMode channel.OverflowMode
	ratePerSec   float64
	fail         channel.ResultClass
	// maxRecipients is what this fake declares about addressing. Zero — the
	// default — means it takes none, which is the behaviour most of the tests
	// here rely on.
	maxRecipients int

	mu         sync.Mutex
	calls      int
	lastMsg    *message.Message
	lastTarget channel.Target
}

// ref is a target reference with no addressing, which is what nearly every
// test here means. Addressable targets are spelled out in full.
func ref(s string) channel.Target { return channel.Target{Ref: s} }

// registerControlled makes a channel type whose factory returns a specific
// instance, so a test can drive the failures directly rather than through
// configuration. Guarded by a mutex because the registry's factory is global
// while the instance is per-test.
var (
	controlledMu   sync.Mutex
	controlledNext *fakeChannel
)

func registerControlled() {
	registerCtlOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type: "routertestctl",
			Capability: channel.Capability{
				SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
			},
			Factory: func(instance string, _ map[string]any) (channel.Channel, error) {
				controlledMu.Lock()
				defer controlledMu.Unlock()
				if controlledNext == nil {
					return nil, errors.New("no controlled channel was set")
				}
				controlledNext.typeName = "routertestctl"
				return controlledNext, nil
			},
		})
	})
}

var registerCtlOnce sync.Once

func registerFake() {
	registerOnce.Do(func() {
		channel.Register(channel.Descriptor{
			Type: "routertest",
			ParamSchema: []channel.ParamSpec{
				{Name: "body_max_len", Type: channel.ParamInt},
				{Name: "formats", Type: channel.ParamStringList},
			},
			Capability: channel.Capability{
				SupportedFormats: []message.Format{message.FormatText, message.FormatMarkdown, message.FormatHTML},
			},
			Factory: func(_ string, cfg map[string]any) (channel.Channel, error) {
				c := &fakeChannel{}
				if n, ok := cfg["body_max_len"].(int); ok {
					c.bodyMaxLen = n
				}
				if fs, ok := cfg["formats"].([]any); ok {
					for _, f := range fs {
						if s, ok := f.(string); ok {
							c.formats = append(c.formats, message.Format(s))
						}
					}
				}
				return c, nil
			},
		})
	})
}

func (c *fakeChannel) Type() string {
	if c.typeName != "" {
		return c.typeName
	}
	return "routertest"
}
func (c *fakeChannel) ParamSchema() []channel.ParamSpec { return nil }
func (c *fakeChannel) Capability() channel.Capability {
	return channel.Capability{
		BodyMaxLen:       c.bodyMaxLen,
		SupportedFormats: c.formats,
		OverflowMode:     c.overflowMode,
		RatePerSec:       c.ratePerSec,
		MaxRecipients:    c.maxRecipients,
	}
}

func (c *fakeChannel) Send(_ context.Context, msg *message.Message, target channel.Target) channel.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.lastMsg = msg
	c.lastTarget = target
	// The zero value means "not configured to fail". It used to be ClassSent;
	// ClassNotAttempted took that slot, and a double that fails by default
	// would be a trap.
	if c.fail != 0 && c.fail != channel.ClassSent {
		return channel.Result{Class: c.fail, Err: errFake, Detail: "fake"}
	}
	return channel.Sent("fake ok")
}

func (c *fakeChannel) Test(context.Context) channel.Result { return channel.Sent("fake ok") }

// ------------------------------------------------------------------- helpers

func textMessage(body string) *message.Message {
	m := &message.Message{Title: "t", Body: body, Format: message.FormatText, Type: message.TypeInfo}
	m.Normalize()
	return m
}

// Each channel declares what it can render; the core does the adapting, so no
// channel implementation ever contains format conversion.
func TestAdapt_DowngradesToWhatTheChannelSupports(t *testing.T) {
	msg := &message.Message{
		Title:  "t",
		Body:   "**bold** and <b>html</b>",
		Format: message.FormatMarkdown,
		Type:   message.TypeInfo,
	}
	msg.Normalize()

	t.Run("channel renders markdown unchanged", func(t *testing.T) {
		got := Adapt(msg, channel.Capability{SupportedFormats: []message.Format{message.FormatMarkdown}})
		if got.Format != message.FormatMarkdown || got.Body != msg.Body {
			t.Errorf("markdown channel got %q (%s)", got.Body, got.Format)
		}
	})

	t.Run("markdown is rendered up to HTML", func(t *testing.T) {
		got := Adapt(msg, channel.Capability{SupportedFormats: []message.Format{message.FormatText, message.FormatHTML}})
		if got.Format != message.FormatHTML {
			t.Fatalf("format = %s, want html", got.Format)
		}
		if !strings.Contains(got.Body, "<strong>bold</strong>") {
			t.Errorf("markdown was not rendered: %q", got.Body)
		}
	})

	t.Run("markdown is flattened for a text-only channel", func(t *testing.T) {
		got := Adapt(msg, channel.Capability{SupportedFormats: []message.Format{message.FormatText}})
		if got.Format != message.FormatText {
			t.Fatalf("format = %s, want text", got.Format)
		}
		if strings.Contains(got.Body, "**") {
			t.Errorf("markdown syntax survived: %q", got.Body)
		}
	})

	t.Run("html is flattened for a text-only channel", func(t *testing.T) {
		htmlMsg := &message.Message{Title: "t", Body: "<p>hello <b>world</b></p>", Format: message.FormatHTML}
		htmlMsg.Normalize()

		got := Adapt(htmlMsg, channel.Capability{SupportedFormats: []message.Format{message.FormatText}})
		if got.Format != message.FormatText {
			t.Fatalf("format = %s, want text", got.Format)
		}
		if strings.Contains(got.Body, "<") {
			t.Errorf("markup survived: %q", got.Body)
		}
		if !strings.Contains(got.Body, "hello") || !strings.Contains(got.Body, "world") {
			t.Errorf("content was lost: %q", got.Body)
		}
	})

	t.Run("a channel that declares nothing is treated as text-only", func(t *testing.T) {
		got := Adapt(msg, channel.Capability{})
		if got.Format != message.FormatText {
			t.Errorf("format = %s, want text (a channel that forgot to declare formats must get the safe option)", got.Format)
		}
	})
}

// ------------------------------------------------------------------- overflow

// totalRunes is what the platform receives: the title the channel renders,
// the body, and whatever markup the channel wraps around them. The declared
// limit is a promise about this number, not about the body alone.
func totalRunes(p *message.Message, cap channel.Capability) int {
	n := utf8.RuneCountInString(p.Title) + utf8.RuneCountInString(p.Body) + cap.PayloadOverheadRunes
	// Links are appended after the body and count against the same limit.
	for _, l := range p.Links {
		n += utf8.RuneCountInString(l.Text) + utf8.RuneCountInString(l.URL) + 4
	}
	return n
}

func assertWithinLimit(t *testing.T, parts []*message.Message, cap channel.Capability, body string) {
	t.Helper()

	var rebuilt strings.Builder
	for i, p := range parts {
		if n := totalRunes(p, cap); n > cap.BodyMaxLen {
			t.Errorf("part %d totals %d characters including its title and markup, over the %d limit",
				i, n, cap.BodyMaxLen)
		}
		rebuilt.WriteString(p.Body)
	}

	if got := utf8.RuneCountInString(rebuilt.String()); got != utf8.RuneCountInString(body) {
		t.Errorf("splitting kept %d of %d characters", got, utf8.RuneCountInString(body))
	}
}

func TestApply_EnforcesTheChannelsLimits(t *testing.T) {
	long := strings.Repeat("x", 1000)

	t.Run("splits an overlong body", func(t *testing.T) {
		cap := channel.Capability{BodyMaxLen: 100, OverflowMode: channel.OverflowSplit}

		parts, err := Apply(textMessage(long), cap)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(parts) < 2 {
			t.Fatalf("an overlong body should be split, got %d part(s)", len(parts))
		}

		assertWithinLimit(t, parts, cap, long)

		if !strings.Contains(parts[0].Title, "[1/") {
			t.Errorf("a split message should say so in the title, got %q", parts[0].Title)
		}
	})

	t.Run("truncates when asked to", func(t *testing.T) {
		cap := channel.Capability{BodyMaxLen: 100, OverflowMode: channel.OverflowTruncate}

		parts, err := Apply(textMessage(long), cap)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(parts) != 1 {
			t.Fatalf("got %d parts, want 1", len(parts))
		}
		if n := totalRunes(parts[0], cap); n > cap.BodyMaxLen {
			t.Errorf("the truncated message totals %d characters, over the %d limit", n, cap.BodyMaxLen)
		}
		if n := utf8.RuneCountInString(parts[0].Body); n >= utf8.RuneCountInString(long) {
			t.Errorf("nothing was truncated: body is still %d characters", n)
		}
	})

	t.Run("rejects when asked to", func(t *testing.T) {
		cap := channel.Capability{BodyMaxLen: 100, OverflowMode: channel.OverflowError}
		if _, err := Apply(textMessage(long), cap); err == nil {
			t.Error("expected an error rather than a silently mangled body")
		}
	})

	t.Run("no limit means no touch", func(t *testing.T) {
		parts, err := Apply(textMessage(long), channel.Capability{})
		if err != nil || len(parts) != 1 || parts[0].Body != long {
			t.Errorf("an unlimited channel must receive the body unchanged (err=%v, parts=%d)", err, len(parts))
		}
	})

	// Counting bytes rather than characters would split a Chinese message far
	// too early and could cut a character in half.
	t.Run("counts characters not bytes", func(t *testing.T) {
		body := strings.Repeat("中", 300) // 900 bytes, 300 runes
		cap := channel.Capability{BodyMaxLen: 100, OverflowMode: channel.OverflowSplit}

		parts, err := Apply(textMessage(body), cap)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertWithinLimit(t, parts, cap, body)

		for _, p := range parts {
			if strings.ContainsRune(p.Body, '�') {
				t.Error("a character was cut in half")
			}
		}
	})

	// A title long enough to consume the whole budget leaves nothing for the
	// body; saying so is better than emitting an empty message.
	t.Run("rejects a title that fills the limit", func(t *testing.T) {
		msg := textMessage("body")
		msg.Title = strings.Repeat("t", 200)

		cap := channel.Capability{BodyMaxLen: 100, OverflowMode: channel.OverflowSplit}
		if _, err := Apply(msg, cap); err == nil {
			t.Error("expected an error when the title alone exceeds the limit")
		}
	})

	// The channel's own markup counts against the limit too.
	t.Run("accounts for channel markup", func(t *testing.T) {
		body := strings.Repeat("x", 200)
		cap := channel.Capability{
			BodyMaxLen: 100, OverflowMode: channel.OverflowSplit,
			PayloadOverheadRunes: 30,
		}

		parts, err := Apply(textMessage(body), cap)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertWithinLimit(t, parts, cap, body)
	})
}

// ------------------------------------------------------------------ the router

func newTestRouter(t *testing.T, cfgs ...config.ChannelConfig) *Router {
	t.Helper()
	registerFake()

	r, err := New(Options{Channels: cfgs, DeliverTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNew_RejectsAnUnknownParameter(t *testing.T) {
	registerFake()

	_, err := New(Options{
		Channels: []config.ChannelConfig{{
			Name: "x", Type: "routertest",
			Config: map[string]any{"body_max_lenght": 100}, // typo
		}},
		DeliverTimeout: time.Second,
	})

	if err == nil {
		t.Fatal("a typo'd parameter must fail startup, not be silently ignored")
	}
	if !strings.Contains(err.Error(), "body_max_lenght") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
}

func TestDeliver_UnknownTargetIsPermanent(t *testing.T) {
	r := newTestRouter(t, config.ChannelConfig{Name: "known", Type: "routertest"})

	res := r.Deliver(context.Background(), "req", ref("nope"), textMessage("b"))
	if res.Class() != channel.ClassPermanent {
		t.Errorf("class = %v, want PERMANENT", res.Class())
	}
	if res.Status != "permanent" {
		t.Errorf("status = %q, want permanent", res.Status)
	}
}

func TestDeliver_TypeQualifiedTargetMustMatch(t *testing.T) {
	r := newTestRouter(t, config.ChannelConfig{Name: "known", Type: "routertest"})

	if res := r.Deliver(context.Background(), "req", ref("routertest:known"), textMessage("b")); res.Class() != channel.ClassSent {
		t.Errorf("matching type qualifier should route: %v (%v)", res.Class(), res.Error)
	}
	if res := r.Deliver(context.Background(), "req", ref("email:known"), textMessage("b")); res.Class() != channel.ClassPermanent {
		t.Errorf("a mismatched type qualifier must be rejected, got %v", res.Class())
	}
}

// A URL target naming a scheme no registered channel answers is refused by
// name, rather than falling through to the alias path and being reported as an
// instance called "//ops@example.com".
//
// This replaces the test that asserted *every* URL target was refused. The
// resolution itself is covered in target_test.go, which registers a channel
// that actually claims a scheme.
func TestDeliver_UnknownURLSchemeIsPermanent(t *testing.T) {
	r := newTestRouter(t, config.ChannelConfig{Name: "known", Type: "routertest"})

	res := r.Deliver(context.Background(), "req", ref("gopher://ops@example.com"), textMessage("b"))
	if res.Class() != channel.ClassPermanent {
		t.Errorf("class = %v, want PERMANENT", res.Class())
	}
	if !strings.Contains(res.Error, "gopher") {
		t.Errorf("the error should name the scheme it could not place, got: %q", res.Error)
	}
}

func TestDeliver_RateLimitSpacesDeliveries(t *testing.T) {
	registerFake()

	r, err := New(Options{
		Channels:       []config.ChannelConfig{{Name: "slow", Type: "routertest"}},
		DeliverTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Five messages at 20/s: the first is immediate, the rest are spaced 50ms.
	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := r.limiter.wait(context.Background(), "slow", 20); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	elapsed := time.Since(start)

	if elapsed < 150*time.Millisecond {
		t.Errorf("five messages at 20/s took %v; the limiter did not space them", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Errorf("five messages at 20/s took %v; the limiter is far too slow", elapsed)
	}
}

func TestDeliver_NoRateLimitCostsNothing(t *testing.T) {
	l := newLimiter()

	start := time.Now()
	for i := 0; i < 1000; i++ {
		if err := l.wait(context.Background(), "fast", 0); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("an unlimited channel took %v for 1000 messages", elapsed)
	}

	// Nothing should have been allocated for a channel with no limit.
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) != 0 {
		t.Errorf("a channel with no rate limit should not create a bucket, got %d", len(l.buckets))
	}
}

func TestLimiter_RespectsContextCancellation(t *testing.T) {
	l := newLimiter()

	// Exhaust the first slot, then cancel while waiting for the next.
	if err := l.wait(context.Background(), "k", 1); err != nil {
		t.Fatalf("first wait: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := l.wait(ctx, "k", 1); err == nil {
		t.Error("a wait that outlives its context must return the context error")
	}
}

// ------------------------------------------------------------------ the rule

// The abstraction's entire value is that the core does not know which channels
// exist. Enforced mechanically, because "remember not to hardcode a channel"
// is not a rule anybody remembers under deadline.
func TestCorePackagesNameNoChannel(t *testing.T) {
	// Quoted, because that is how a channel name appears in code that has
	// started special-casing one: switch ch.Type() { case "email": ...
	forbidden := []string{`"email"`, `"slack"`, `"webhook"`, `"dingtalk"`, `"feishu"`, `"wecom"`}

	// The target scheme counts as a channel name for this rule. It is not in
	// the list above because it is a different word, which is exactly why it
	// has to be written down: a `case "mailto":` in the router would pass every
	// other assertion here while making the core depend on one channel just as
	// surely. The router dispatches schemes through channel.LookupScheme and
	// never compares one itself.
	forbidden = append(forbidden, `"mailto"`)

	dirs := []string{".", "../api", "../message", "../smtpin"}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}

			source, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}

			for _, word := range forbidden {
				if strings.Contains(string(source), word) {
					t.Errorf("%s/%s names the channel %s; the core must route through the registry only",
						dir, name, word)
				}
			}
		}
	}
}

var errFake = errors.New("fake failure")
