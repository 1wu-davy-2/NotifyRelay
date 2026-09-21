// Package render converts message bodies between markup formats.
//
// Two consumers need this: the router, which adapts a message to whatever a
// channel can display, and the mail composer, which must supply a plain-text
// alternative alongside an HTML body. Keeping the conversions here means the
// regexes are defined once instead of drifting apart between callers.
package render

import (
	"bytes"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
)

var markdown = goldmark.New()

// MarkdownToHTML renders markdown as an HTML fragment.
//
// On the (practically unreachable) renderer error it falls back to escaped
// preformatted text: a delivery must never fail because of a rendering quirk.
func MarkdownToHTML(s string) string {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(s), &buf); err != nil {
		return "<pre>" + html.EscapeString(s) + "</pre>"
	}
	return strings.TrimSpace(buf.String())
}

// RE2 has no backreferences, so paired delimiters are matched with separate
// patterns rather than one pattern with a \1.
var (
	reMDCodeFence  = regexp.MustCompile("(?s)```[^\n]*\n?(.*?)```")
	reMDCodeInline = regexp.MustCompile("`([^`]*)`")
	reMDImage      = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reMDLink       = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reMDHeading    = regexp.MustCompile(`(?m)^[ \t]{0,3}#{1,6}[ \t]*`)
	reMDQuote      = regexp.MustCompile(`(?m)^[ \t]{0,3}>[ \t]?`)
	reMDListMarker = regexp.MustCompile(`(?m)^[ \t]*(?:[-*+]|\d+\.)[ \t]+`)
	// Spelled out as three alternatives because RE2 has no backreferences.
	reMDRule      = regexp.MustCompile(`(?m)^[ \t]{0,3}(?:-{3,}|\*{3,}|_{3,})[ \t]*$`)
	reMDBoldStar  = regexp.MustCompile(`\*\*(.+?)\*\*`)
	reMDBoldUnder = regexp.MustCompile(`__(.+?)__`)
	reMDStrike    = regexp.MustCompile(`~~(.+?)~~`)
	reMDItalicStar = regexp.MustCompile(`\*(.+?)\*`)
	reMDItalicUnd  = regexp.MustCompile(`_(.+?)_`)
)

// MarkdownToText strips markdown syntax, keeping the readable text.
func MarkdownToText(s string) string {
	s = reMDCodeFence.ReplaceAllString(s, "$1")
	s = reMDImage.ReplaceAllString(s, "$1")
	s = reMDLink.ReplaceAllString(s, "$1")
	s = reMDRule.ReplaceAllString(s, "")
	s = reMDHeading.ReplaceAllString(s, "")
	s = reMDQuote.ReplaceAllString(s, "")
	s = reMDListMarker.ReplaceAllString(s, "")
	s = reMDCodeInline.ReplaceAllString(s, "$1")
	s = reMDBoldStar.ReplaceAllString(s, "$1")
	s = reMDBoldUnder.ReplaceAllString(s, "$1")
	s = reMDStrike.ReplaceAllString(s, "$1")
	s = reMDItalicStar.ReplaceAllString(s, "$1")
	s = reMDItalicUnd.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

var (
	reHTMLScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reHTMLStyle  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reHTMLBreak  = regexp.MustCompile(`(?i)<(?:br|/p|/div|/li|/h[1-6]|/tr|/table|/blockquote|/pre)\b[^>]*>`)
	reHTMLTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reHTMLSpaces = regexp.MustCompile(`[ \t]+\n`)
	reHTMLBlanks = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText strips markup and entities, keeping the readable text.
//
// This is what gives an HTML mail its plain-text alternative, so it must
// produce something a human can read in a text-only client — not a tag soup.
func HTMLToText(s string) string {
	s = reHTMLScript.ReplaceAllString(s, "")
	s = reHTMLStyle.ReplaceAllString(s, "")
	s = reHTMLBreak.ReplaceAllString(s, "\n")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reHTMLSpaces.ReplaceAllString(s, "\n")
	s = reHTMLBlanks.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
