package render

import (
	"strings"
	"testing"
)

func TestMarkdown_CommonMarkIsUnchanged(t *testing.T) {
	body := "**bold** and [text](https://example.com) and # Heading"

	if got := Markdown(body, DialectCommonMark); got != body {
		t.Errorf("CommonMark should pass through unchanged, got %q", got)
	}
}

// Slack's mrkdwn is not markdown. Sending CommonMark to it does not fail; it
// just puts literal asterisks and hashes in front of a reader.
func TestMarkdown_Slack(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bold", in: "**bold**", want: "*bold*"},
		{name: "bold with underscores", in: "__bold__", want: "*bold*"},
		{name: "italic", in: "*italic*", want: "_italic_"},
		{name: "strikethrough", in: "~~gone~~", want: "~gone~"},
		{
			name: "link",
			in:   "see [the docs](https://example.com/doc)",
			want: "see <https://example.com/doc|the docs>",
		},
		{
			name: "heading becomes bold",
			in:   "## Latency",
			want: "*Latency*",
		},
		{
			name: "multiple headings",
			in:   "# One\n\ntext\n\n### Three",
			want: "*One*\n\ntext\n\n*Three*",
		},
		{
			// ** must be consumed before * is considered, or bold turns into
			// nested italics.
			name: "bold and italic together",
			in:   "**bold** then *italic*",
			want: "*bold* then _italic_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Markdown(tt.in, DialectSlack); got != tt.want {
				t.Errorf("Markdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Neither Feishu's lark_md nor WeCom's markdown renders '#' headings.
func TestMarkdown_HeadingsBecomeBold(t *testing.T) {
	for _, dialect := range []Dialect{DialectFeishu, DialectWeCom} {
		t.Run(string(dialect), func(t *testing.T) {
			got := Markdown("# Alert\n\nbody text", dialect)

			if strings.Contains(got, "#") {
				t.Errorf("a heading survived: %q", got)
			}
			if !strings.Contains(got, "*Alert*") {
				t.Errorf("the heading text should be bolded: %q", got)
			}
			// Bold and links already match these platforms' syntax.
			if got := Markdown("**bold**", dialect); got != "**bold**" {
				t.Errorf("bold should be left alone, got %q", got)
			}
		})
	}
}

// DingTalk's markdown is close enough to CommonMark that nothing is rewritten.
func TestMarkdown_DingTalkIsUnchanged(t *testing.T) {
	body := "# Heading\n\n**bold** and [text](https://example.com)"

	if got := Markdown(body, DialectDingTalk); got != body {
		t.Errorf("DingTalk markdown should pass through, got %q", got)
	}
}

func TestMarkdown_EmptyBodies(t *testing.T) {
	for _, dialect := range []Dialect{DialectCommonMark, DialectSlack, DialectDingTalk, DialectFeishu, DialectWeCom} {
		if got := Markdown("", dialect); got != "" {
			t.Errorf("Markdown(\"\", %q) = %q, want empty", dialect, got)
		}
	}
}

func TestMarkdown_UnknownDialectPassesThrough(t *testing.T) {
	body := "**bold** and # heading"

	// An unrecognised dialect must not mangle the body: leaving it alone is a
	// cosmetic problem, rewriting it wrongly is a correctness one.
	if got := Markdown(body, Dialect("mattermost")); got != body {
		t.Errorf("unknown dialect should pass through, got %q", got)
	}
}

func TestDialect_Known(t *testing.T) {
	known := []Dialect{DialectCommonMark, DialectSlack, DialectDingTalk, DialectFeishu, DialectWeCom}
	for _, d := range known {
		if !d.Known() {
			t.Errorf("%q should be known", d)
		}
	}
	if Dialect("telegram").Known() {
		t.Error("an unhandled dialect should not report itself as known")
	}
}
