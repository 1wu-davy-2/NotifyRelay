package admin

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"notifyrelay/internal/admin/i18n"
)

// minSampleCodeLines is the floor below which the comment stripper is presumed
// to have eaten the sample rather than the sample being mostly prose.
//
// curl is the shortest of the seven at fourteen lines of code, so this is a
// floor with room under it rather than a number chosen to make the test pass.
const minSampleCodeLines = 10

// commentMarkers are how a comment starts, in the seven languages the samples
// are written in.
//
// `--` is deliberately not one of them, though SQL and Lua use it. curl's long
// options start with it, so treating it as a comment marker makes every
// `--max-time`, `--fail-with-body` and `--silent` invisible to the comparison
// below — which is most of what the curl sample actually does. That was the
// first version of this list, and a mutation that changed `--max-time 10` to
// `--max-time 20` in the Chinese copy passed it.
var commentMarkers = []string{"#", "//", "/*", "*", "*/"}

// pyDocString matches a Python triple-quoted string.
//
// It is not a comment by any of the markers above — it is a string literal —
// but the samples use it for module and function documentation, which is prose
// and has to be translatable. Left in, the Python sample's TLS warning would be
// the one paragraph on the page that stayed English.
var pyDocString = regexp.MustCompile(`(?s)""".*?"""`)

// codeLines drops the prose and keeps everything else byte for byte.
//
// Deliberately line-based and deliberately dumb. A real parser per language
// would be seven parsers to maintain and seven ways to be subtly wrong, and
// what this needs to catch is somebody editing the code in one language and not
// the other. A line that starts with one of these markers is a comment in all
// seven of these files.
//
// The rule can be wrong in one direction: a line of code that happens to begin
// with `*` is dropped. It is dropped from both languages equally, so the
// comparison still holds — the rule only makes the check slightly weaker, and
// minSampleCodeLines is what catches it going wrong wholesale.
//
// A trailing comment — `x := 1 // why` — is part of the code line and is
// compared whole, so it cannot be translated without the comparison losing its
// grip on that line. Four of them existed and were moved onto their own lines
// in both languages rather than teaching this function to parse string
// literals. Two are left, deliberately:
//
//	}  // namespace            a C++ convention, not translated in C++ either
//	/* NULL for a GET */       names a C constant and an HTTP method
//
// A new one should be moved rather than left; the alternative is another
// English sentence on a Chinese page.
func codeLines(src string) []string {
	src = pyDocString.ReplaceAllString(src, "")

	var out []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, marker := range commentMarkers {
			if strings.HasPrefix(trimmed, marker) {
				goto skip
			}
		}
		out = append(out, line)
	skip:
	}
	return out
}

// mustReadSample reads one language's copy of one sample.
func mustReadSample(t *testing.T, l i18n.Lang, id string) string {
	t.Helper()

	body, err := fs.ReadFile(assets, "samples/"+string(l)+"/"+id+".txt")
	if err != nil {
		t.Fatalf("reading the %s sample in %s: %v", id, l, err)
	}
	return string(body)
}

// The languages' samples are the same program with different comments.
//
// Duplicating a file per language is how the two drift, and the drift that
// matters is the code: somebody fixes a header or a status code in one copy and
// the other keeps the old one, and the reader who gets the stale one has no way
// to know. The comments are the translation; the code is not.
func TestSamples_EveryLanguageIsTheSameCode(t *testing.T) {
	for _, s := range sampleFiles {
		want := codeLines(mustReadSample(t, i18n.EN, s.ID))
		if len(want) < minSampleCodeLines {
			t.Fatalf("%s: only %d lines of code survived the comment stripper — "+
				"the stripper is eating the sample, so this test proves nothing",
				s.ID, len(want))
		}

		for _, l := range i18n.Langs {
			if l == i18n.EN {
				continue
			}

			got := codeLines(mustReadSample(t, l, s.ID))
			if len(got) != len(want) {
				t.Errorf("%s/%s: %d lines of code, want %d — the code differs, not only the comments",
					l, s.ID, len(got), len(want))
				continue
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("%s/%s: line %d of the code differs\n  en: %q\n  %s: %q",
						l, s.ID, i+1, want[i], l, got[i])
				}
			}
		}
	}
}

// Every language has every sample.
//
// loadSamples already fails to start without one, which is the real guard — it
// is much easier to notice a service that will not start than a tab that
// quietly shows another language. This says so directly, so the failure names
// the missing file rather than surfacing as a panic at init.
func TestSamples_EveryLanguageHasEverySample(t *testing.T) {
	for _, l := range i18n.Langs {
		for _, s := range sampleFiles {
			if strings.TrimSpace(mustReadSample(t, l, s.ID)) == "" {
				t.Errorf("%s/%s is empty", l, s.ID)
			}
		}
	}
}

// The samples are not the same in both languages, or the translation did not
// happen.
//
// A copy of the English file at the Chinese path passes every check above and
// leaves the page exactly as English as it was. This is the check that says the
// work was done.
func TestSamples_AreActuallyTranslated(t *testing.T) {
	for _, l := range i18n.Langs {
		if l == i18n.EN {
			continue
		}
		for _, s := range sampleFiles {
			en := mustReadSample(t, i18n.EN, s.ID)
			got := mustReadSample(t, l, s.ID)
			if got == en {
				t.Errorf("%s/%s is byte-identical to the English one", l, s.ID)
			}
		}
	}
}
