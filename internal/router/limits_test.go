package router

import (
	"strings"
	"testing"
	"unicode/utf8"

	// The real registered capabilities, so these limits are the ones the
	// channels actually declare rather than numbers copied into a test.
	_ "notifyrelay/internal/channel/all"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

// A body that overflows every IM channel must come out split, with no chunk
// over either limit, and with nothing dropped along the way.
//
// The byte limit is the one that matters here: DingTalk caps a robot message
// at 20000 bytes and WeCom at 4096, and 10000 Chinese characters is 30000
// bytes. A splitter counting only characters would produce chunks that look
// fine and are rejected by the platform.
func TestApply_SplitsForEveryRealChannel(t *testing.T) {
	const bodyRunes = 10000
	body := strings.Repeat("中", bodyRunes)

	seen := 0

	for _, d := range channel.Descriptors() {
		if d.Capability.OverflowMode != channel.OverflowSplit {
			continue
		}
		seen++

		t.Run(d.Type, func(t *testing.T) {
			cap := d.Capability

			parts, err := Apply(textMessage(body), cap)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if len(parts) < 2 {
				t.Fatalf("a %d-character body produced %d part(s) for a channel limited to %d runes / %d bytes",
					bodyRunes, len(parts), cap.BodyMaxLen, cap.BodyMaxBytes)
			}

			var rebuilt strings.Builder
			for i, p := range parts {
				if cap.BodyMaxLen > 0 {
					if n := utf8.RuneCountInString(p.Body); n > cap.BodyMaxLen {
						t.Errorf("part %d is %d characters, over the %d limit", i, n, cap.BodyMaxLen)
					}
				}
				if cap.BodyMaxBytes > 0 {
					if n := len(p.Body); n > cap.BodyMaxBytes {
						t.Errorf("part %d is %d bytes, over the %d limit", i, n, cap.BodyMaxBytes)
					}
				}
				if strings.ContainsRune(p.Body, '�') {
					t.Errorf("part %d cut a character in half", i)
				}
				rebuilt.WriteString(p.Body)
			}

			if rebuilt.Len() == 0 {
				t.Fatal("splitting produced no content")
			}
			if got := utf8.RuneCountInString(rebuilt.String()); got != bodyRunes {
				t.Errorf("splitting kept %d of %d characters", got, bodyRunes)
			}
		})
	}

	if seen == 0 {
		t.Fatal("no channel declares split overflow; this test would pass vacuously")
	}
}

// The declared limits have to be the platform's real ones, or the split above
// proves nothing about what the platform will accept.
func TestDeclaredLimitsMatchThePlatforms(t *testing.T) {
	want := map[string]struct {
		maxBytes int
		maxRunes int
	}{
		// DingTalk: a robot message is capped at 20000 bytes.
		"dingtalk": {maxBytes: 20000, maxRunes: 5000},
		// WeCom: markdown content is capped at 4096 bytes.
		"wecom": {maxBytes: 4096, maxRunes: 1300},
		// Slack truncates around 4000 characters.
		"slack": {maxRunes: 4000},
	}

	for typ, expected := range want {
		d, ok := channel.Lookup(typ)
		if !ok {
			t.Errorf("channel %q is not registered", typ)
			continue
		}

		if expected.maxBytes > 0 && d.Capability.BodyMaxBytes != expected.maxBytes {
			t.Errorf("%s byte limit = %d, want %d", typ, d.Capability.BodyMaxBytes, expected.maxBytes)
		}
		if expected.maxRunes > 0 && d.Capability.BodyMaxLen != expected.maxRunes {
			t.Errorf("%s rune limit = %d, want %d", typ, d.Capability.BodyMaxLen, expected.maxRunes)
		}

		// A rune limit that could still blow the byte limit is not a limit:
		// four bytes is the worst case for one character in UTF-8.
		if d.Capability.BodyMaxBytes > 0 && d.Capability.BodyMaxLen > 0 {
			if worst := d.Capability.BodyMaxLen * 4; worst < d.Capability.BodyMaxBytes {
				// Not an error by itself, but it means the rune limit binds
				// first for every input — worth knowing, not worth failing.
				t.Logf("%s: the rune limit binds before the byte limit (worst case %d bytes < %d)",
					typ, worst, d.Capability.BodyMaxBytes)
			}
		}
	}
}

// Every channel that speaks markdown must name its dialect, or CommonMark
// syntax reaches readers literally.
func TestMarkdownChannelsDeclareADialect(t *testing.T) {
	for _, d := range channel.Descriptors() {
		if !d.Capability.Supports(message.FormatMarkdown) {
			continue
		}
		if !d.Capability.MarkdownDialect.Known() {
			t.Errorf("%s supports markdown but declares dialect %q",
				d.Type, d.Capability.MarkdownDialect)
		}
	}
}
