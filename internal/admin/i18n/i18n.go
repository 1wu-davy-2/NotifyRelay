// Package i18n holds the operator interface's copy, in every language it ships.
//
// The table is a struct rather than a map, and that is the whole design. A map
// with a missing key renders as an empty string — a button with no label, a
// heading that is not there — and nothing fails until somebody opens that page
// in that language, which is a bug report from a deployment nobody on the team
// can read. A struct with a missing field does not compile.
//
// What that costs is a field for every string, written twice. That is the price
// of the failure mode being a build error rather than a blank page.
package i18n

import (
	"fmt"
	"strings"
	"time"
)

// Lang is a language tag.
type Lang string

const (
	// ZH is Simplified Chinese.
	ZH Lang = "zh"
	// EN is English.
	EN Lang = "en"
)

// Langs is what this build ships, in the order the switcher shows them.
var Langs = []Lang{ZH, EN}

// Default is what a first-time visitor gets.
//
// Chinese, because the people who deploy this read Chinese and the English
// interface was the thing that prompted this work. It is a default and not a
// setting: the switch is one click and the choice is remembered.
const Default = ZH

// CookieName is where the choice is kept.
//
// A cookie rather than a session field, because the sign-in page and the
// first-run page both need a switcher and neither has a session yet.
const CookieName = "nr_lang"

// Param is the query parameter that overrides the cookie for one request.
//
// It exists so a link can pin a language — a screenshot, a bug report, a
// bookmark — and it is not how the choice is kept. Keeping it in the query
// string alone was the previous implementation's mistake: the first link the
// operator clicked dropped them back into English.
const Param = "lang"

// CookieMaxAge is how long a language choice lasts. A year: it is a preference,
// not a credential, and re-asking is the annoyance this exists to remove.
const CookieMaxAge = 365 * 24 * 60 * 60

// Parse resolves a tag to a language this build has.
//
// Anything unrecognised is the default rather than an error. A language the
// interface does not have is not a failure — it is a fallback, and the
// alternative is a 400 on a page whose whole purpose is to be readable.
func Parse(tag string) Lang {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "zh", "zh-cn", "zh-hans", "cn", "chinese":
		return ZH
	case "en", "en-us", "en-gb", "english":
		return EN
	}
	return Default
}

// For returns the table for a language. The pointer is to a package-level value,
// so a table is built once at startup rather than per request.
func For(l Lang) *Messages {
	if l == EN {
		return &en
	}
	return &zh
}

// Name is the language's name in its own language.
//
// Deliberately not translated. The person the switcher is for is the one who
// cannot read the page they are on, and labelling the way out in the language
// they cannot read is labelling it for everybody except them.
func (l Lang) Name() string {
	if l == EN {
		return "English"
	}
	return "中文"
}

// HTMLTag is the value for the html element's lang attribute.
//
// The region matters for the browser's font fallback and for a screen reader's
// pronunciation, and "zh" alone leaves both to guess between Simplified and
// Traditional.
func (l Lang) HTMLTag() string {
	if l == EN {
		return "en"
	}
	return "zh-CN"
}

// Other returns the language a switcher link should offer.
func (l Lang) Other() Lang {
	if l == EN {
		return ZH
	}
	return EN
}

// ------------------------------------------------------------------- copy

// Since renders how long ago a time was, the way somebody reading a log wants
// it: blank for an unset field rather than a count of years since year zero.
//
// This is here rather than in the template's FuncMap because it is copy and not
// formatting: the Chinese form has no plural and counts up in a different order,
// so it is a different sentence rather than the same one with the words
// swapped. A FuncMap closure over the language would do the same job; putting it
// on the table is what makes that obvious.
//
// The signature takes a time.Time, which is what a template has. It took a
// Duration for one revision, and a template function whose argument type does
// not match is a runtime error rather than a compile error — every page using
// it rendered as a truncated document with the error only in the log.
func (m *Messages) Since(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	switch d := time.Since(t); {
	case d < time.Minute:
		return m.TimeJustNow
	case d < time.Hour:
		return fmt.Sprintf(m.TimeMinutesAgo, int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf(m.TimeHoursAgo, int(d.Hours()))
	default:
		return fmt.Sprintf(m.TimeDaysAgo, int(d.Hours()/24))
	}
}

// Stamp renders a timestamp. Identical in both languages today, and kept here so
// that the day one of them wants a different format it is not a template change.
func (m *Messages) Stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
