package render

import (
	"regexp"
	"strings"
)

// Dialect is a chat platform's markdown flavour.
//
// "Markdown" is not one format. Slack's mrkdwn uses *single stars* for bold and
// <angle|brackets> for links; DingTalk and Feishu do not render '#' headings at
// all. Sending CommonMark to them does not fail — it just puts literal
// asterisks and hashes in front of a human, which is worse than failing.
//
// The conversion lives in this package rather than in each channel so that a
// channel author declares a dialect instead of writing a converter, and so the
// differences are visible in one place.
type Dialect string

const (
	// DialectCommonMark is standard markdown, rendered unchanged.
	DialectCommonMark Dialect = ""

	DialectSlack    Dialect = "slack"
	DialectDingTalk Dialect = "dingtalk"
	DialectFeishu   Dialect = "feishu"
	DialectWeCom    Dialect = "wecom"
)

// Known reports whether the dialect is one this package handles.
func (d Dialect) Known() bool {
	switch d {
	case DialectCommonMark, DialectSlack, DialectDingTalk, DialectFeishu, DialectWeCom:
		return true
	default:
		return false
	}
}

// Markdown converts CommonMark into the requested dialect.
func Markdown(body string, d Dialect) string {
	switch d {
	case DialectCommonMark:
		return body
	case DialectDingTalk:
		// DingTalk renders headings, bold and links close enough to CommonMark
		// that nothing needs rewriting.
		return body
	case DialectSlack:
		return toSlackMrkdwn(body)
	case DialectFeishu, DialectWeCom:
		return toHeadinglessBold(body)
	default:
		return body
	}
}

// toSlackMrkdwn converts CommonMark to Slack's mrkdwn.
//
//	**bold**        -> *bold*
//	_italic_        -> _italic_   (unchanged)
//	~~strike~~      -> ~strike~
//	[text](url)     -> <url|text>
//	# Heading       -> *Heading*  (mrkdwn has no headings)
func toSlackMrkdwn(body string) string {
	s := body

	s = reMDImage.ReplaceAllString(s, "$1")
	s = reMDLinkFull.ReplaceAllStringFunc(s, func(m string) string {
		parts := reMDLinkFull.FindStringSubmatch(m)
		text, url := parts[1], parts[2]
		if text == "" {
			return "<" + url + ">"
		}
		return "<" + url + "|" + text + ">"
	})

	// One pass per delimiter, with bold and italic handled by the same
	// pattern. Converting bold first and italic second would let the italic
	// rule re-process the asterisks bold had just produced, turning *bold*
	// into _bold_.
	s = reMDStarEmphasis.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "**") {
			return "*" + m[2:len(m)-2] + "*"
		}
		return "_" + m[1:len(m)-1] + "_"
	})
	s = reMDUnderEmphasis.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "__") {
			return "*" + m[2:len(m)-2] + "*"
		}
		return "_" + m[1:len(m)-1] + "_"
	})

	s = reMDStrike.ReplaceAllString(s, "~$1~")
	s = headingsToBold(s)
	return s
}

// A link pattern that captures both the text and the URL. The plain reMDLink
// used for text flattening captures only the text.
var reMDLinkFull = regexp.MustCompile(`\[([^\]]*)\]\(([^)]*)\)`)

// Emphasis patterns matching bold and italic together, so a single pass can
// rewrite both without one rule consuming the other's output.
var (
	reMDStarEmphasis  = regexp.MustCompile(`\*\*(.+?)\*\*|\*(.+?)\*`)
	reMDUnderEmphasis = regexp.MustCompile(`__(.+?)__|_(.+?)_`)
)

// toHeadinglessBold converts headings to bold lines for platforms whose
// markdown has no heading syntax (Feishu's lark_md, WeCom).
func toHeadinglessBold(body string) string {
	return headingsToBold(body)
}

var reMDHeadingLine = regexp.MustCompile(`(?m)^[ \t]{0,3}(#{1,6})[ \t]*(.*)$`)

func headingsToBold(s string) string {
	return reMDHeadingLine.ReplaceAllStringFunc(s, func(line string) string {
		m := reMDHeadingLine.FindStringSubmatch(line)
		text := strings.TrimSpace(m[2])
		if text == "" {
			return ""
		}
		return "*" + text + "*"
	})
}
