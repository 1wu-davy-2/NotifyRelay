package i18n

import (
	"html/template"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Both tables have to be complete.
//
// The compiler guarantees that every field exists in both. It says nothing
// about whether either was left blank, and a blank string compiles and renders
// as nothing at all — a button with no label, a heading that is not there. That
// is invisible to every test that does not go looking for it, which is why this
// one walks the struct rather than naming the fields it happens to know about.
func TestTables_HaveNoEmptyStrings(t *testing.T) {
	for _, tc := range []struct {
		lang Lang
		msg  *Messages
	}{{ZH, &zh}, {EN, &en}} {
		walkStrings(t, string(tc.lang), reflect.ValueOf(tc.msg).Elem(), func(path, value string) {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s is empty", path)
			}
		})
	}
}

// The two languages have to interpolate the same things.
//
// A %s in one and not the other is a bug that renders as "%!s(MISSING)" and
// only appears in the language nobody on the team is testing in — which is
// exactly the failure this whole table exists to prevent.
func TestTables_AgreeOnPlaceholders(t *testing.T) {
	zhv := reflect.ValueOf(&zh).Elem()
	env := reflect.ValueOf(&en).Elem()

	walkPairs(t, "Messages", zhv, env, func(path, zhValue, enValue string) {
		got := placeholder.FindAllString(zhValue, -1)
		want := placeholder.FindAllString(enValue, -1)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: zh interpolates %v, en interpolates %v", path, got, want)
		}
	})
}

// A table's own name has to match the tag it is filed under, or the switcher
// offers a link that resolves back to where the reader already is.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Lang
	}{
		{"zh", ZH}, {"zh-CN", ZH}, {"ZH-cn", ZH}, {"cn", ZH},
		{"en", EN}, {"en-US", EN}, {"EN", EN},
		// Anything else is the default rather than an error: a language this
		// build does not have is a fallback, not a failure.
		{"de", Default}, {"", Default}, {"  ", Default}, {"../../etc/passwd", Default},
	} {
		if got := Parse(tc.in); got != tc.want {
			t.Errorf("Parse(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every language the switcher offers must resolve to itself, or the link does
// nothing when it is clicked. The switcher is built from Langs, so this is what
// keeps that list and Parse in agreement.
func TestEveryShippedLanguageResolvesToItself(t *testing.T) {
	if len(Langs) < 2 {
		t.Fatalf("Langs = %v; a switcher with one language is not a switcher", Langs)
	}

	seen := map[Lang]bool{}
	for _, l := range Langs {
		if seen[l] {
			t.Errorf("%q appears twice in Langs", l)
		}
		seen[l] = true

		if got := Parse(string(l)); got != l {
			t.Errorf("Parse(%q) = %q, want itself", l, got)
		}
		if l.Name() == "" || l.HTMLTag() == "" {
			t.Errorf("%q has no display name or html lang tag", l)
		}
		if l.Other() == l {
			t.Errorf("%q switches to itself", l)
		}
	}

	// And the default has to be one of them, or a first-time visitor is served
	// a table the switcher cannot get back to.
	if !seen[Default] {
		t.Errorf("the default %q is not in Langs", Default)
	}
}

// The relative times are copy, not formatting: the Chinese form has no plural
// and counts in a different order. This checks the boundaries, which is where a
// switch over durations goes wrong.
func TestSince(t *testing.T) {
	// Every branch, at a point comfortably inside it rather than on the
	// boundary — the boundaries are the switch's business, and a test that sits
	// on one fails for the wrong reason the day somebody changes < to <=.
	for _, age := range []time.Duration{
		30 * time.Second, 5 * time.Minute, 3 * time.Hour, 72 * time.Hour,
	} {
		at := time.Now().Add(-age)
		for _, l := range Langs {
			got := For(l).Since(at)
			if strings.Contains(got, "%!") {
				t.Errorf("%s: Since(%v ago) = %q — a placeholder was not filled", l, age, got)
			}
			if got == "" {
				t.Errorf("%s: Since(%v ago) is empty", l, age)
			}
		}
	}

	// The units are the language's, not English's.
	if got := For(ZH).Since(time.Now().Add(-5 * time.Minute)); !strings.Contains(got, "分钟") {
		t.Errorf("Chinese Since(5m ago) = %q", got)
	}
	if got := For(EN).Since(time.Now().Add(-5 * time.Minute)); !strings.Contains(got, "m ago") {
		t.Errorf("English Since(5m ago) = %q", got)
	}
}

// An unset time renders as nothing, not as the fifty-odd years since year zero.
//
// The signature matters as much as the behaviour: a template function whose
// argument type does not match what the template passes is a runtime error, and
// the page renders as a truncated document with the error only in the log. The
// types here are the ones the templates actually use.
func TestSinceAndStamp_TakeWhatTheTemplatesPass(t *testing.T) {
	m := For(Default)

	// Assigned to the exact signature the FuncMap needs, so a change to either
	// is a compile error here rather than a broken page in production.
	var _ func(time.Time) string = m.Since
	var _ func(time.Time) string = m.Stamp

	if got := m.Since(time.Time{}); got != "" {
		t.Errorf("Since(zero) = %q, want blank", got)
	}
	if got := m.Stamp(time.Time{}); got != "" {
		t.Errorf("Stamp(zero) = %q, want blank", got)
	}
}

// Markup appears where it is meant to and nowhere else.
//
// The template.HTML fields are rendered unescaped, so what they may contain is
// a security boundary rather than a style preference. It holds today because
// every one of them is a constant written in this package and nothing in them
// comes from a request — which is exactly the kind of property that stops being
// true by accident. So it is checked: a translation that pastes in a script
// tag, an event handler or a link is a failed build rather than a stored
// cross-site scripting bug on the page operators trust most.
//
// The other half matters too. A plain string field that contains a tag is
// escaped by html/template and renders as literal text, so it is a bug even
// though it is not a vulnerability.
func TestMarkup_IsAllowedOnlyWhereItIsDeclared(t *testing.T) {
	tagRE := regexp.MustCompile(`</?\s*([a-zA-Z][a-zA-Z0-9]*)[^>]*>`)
	allowed := map[string]bool{"code": true, "strong": true, "em": true, "br": true}

	for _, tc := range []struct {
		lang Lang
		msg  *Messages
	}{{ZH, &zh}, {EN, &en}} {
		walkFields(t, string(tc.lang), reflect.ValueOf(tc.msg).Elem(),
			func(path, value string, isHTML bool) {
				tags := tagRE.FindAllStringSubmatch(value, -1)

				if !isHTML {
					if len(tags) > 0 {
						t.Errorf("%s is a plain string but contains %q — it will render as literal text",
							path, tags[0][0])
					}
					return
				}

				for _, tag := range tags {
					if !allowed[strings.ToLower(tag[1])] {
						t.Errorf("%s: <%s> is not in the allowed set", path, tag[1])
					}
					// An attribute is where the injection lives: onclick, style,
					// an href with a javascript: URL. None of these needs one.
					if strings.Contains(tag[0], "=") {
						t.Errorf("%s carries an attribute: %q", path, tag[0])
					}
				}
			})
	}
}

// ------------------------------------------------------------------ walking

// placeholder matches the verbs the tables are allowed to use. Anything else —
// a %q, a width — would be a formatting decision in a string a translator is
// meant to be able to reorder, so the test pins the set.
var placeholder = regexp.MustCompile(`%[sd]`)

func walkStrings(t *testing.T, path string, v reflect.Value, fn func(path, value string)) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		name := path + "." + v.Type().Field(i).Name
		switch v.Field(i).Kind() {
		case reflect.Struct:
			walkStrings(t, name, v.Field(i), fn)
		case reflect.String:
			fn(name, v.Field(i).String())
		}
	}
}

// htmlType is the one string type whose value is rendered unescaped.
var htmlType = reflect.TypeOf(template.HTML(""))

// walkFields visits every string field, saying which of them are markup.
func walkFields(t *testing.T, path string, v reflect.Value, fn func(path, value string, isHTML bool)) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		name := path + "." + v.Type().Field(i).Name
		switch v.Field(i).Kind() {
		case reflect.Struct:
			walkFields(t, name, v.Field(i), fn)
		case reflect.String:
			fn(name, v.Field(i).String(), v.Field(i).Type() == htmlType)
		}
	}
}

func walkPairs(t *testing.T, path string, a, b reflect.Value, fn func(path, a, b string)) {
	t.Helper()
	for i := 0; i < a.NumField(); i++ {
		name := path + "." + a.Type().Field(i).Name
		switch a.Field(i).Kind() {
		case reflect.Struct:
			walkPairs(t, name, a.Field(i), b.Field(i), fn)
		case reflect.String:
			fn(name, a.Field(i).String(), b.Field(i).String())
		}
	}
}
