package admin

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The client-side sources keep no English prose of their own.
//
// ---------------------------------------------------------------------------
// A page rendered in Chinese carries no English interface copy. That used to be
// enforced by rendering the page and looking for the English table's own
// sentences in it, and by scanning the templates for literals that no lookup
// produced. The page is a React app now, so the first half has nowhere to run —
// but the second half is unchanged in substance and reads a different kind of
// file.
//
// The failure mode is the same and it is still the one that survives review. The
// copy table makes a missing field a TypeScript error, so `t.Foo` where Foo is
// not a field does not compile. What compiles perfectly is `<h1>Channels</h1>`
// sitting where `<h1>{t.TitleChannels}</h1>` belongs, and in a diff it looks
// like it belongs there.
//
// So this reads the sources. Comments and `{...}` expressions come out first,
// then the text between tags and the four attributes that carry copy; anything
// left that is long enough to be a sentence and contains a space is a literal
// that no table lookup produced. Data cannot reach it, which is the point — a
// check on rendered output cannot tell a translated page from a lucky one, and
// this one cannot be fooled by what happens to be in the database.
//
// It does not check the other direction, and cannot: whether a Chinese string
// is a *good* translation is not a property of the source. What it catches is
// the string that was never translated at all, because there is nothing there
// to translate.
// ---------------------------------------------------------------------------
func TestClientSources_KeepNoEnglishProse(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")

	var scanned int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tsx") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++

		rel, _ := filepath.Rel(root, path)
		for _, lit := range proseLiterals(stripTSX(string(body))) {
			t.Errorf("%s keeps the literal %q — it belongs in the copy table",
				filepath.ToSlash(rel), lit)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the client sources: %v", err)
	}

	// The walk is over whatever is in the directory, so a component added later
	// is checked without anybody remembering to add it here. What it cannot do
	// is notice that the directory read came back empty — a path that is wrong,
	// or a tree that moved.
	if scanned == 0 {
		t.Fatalf("no .tsx files under %s; this test would assert nothing", root)
	}
}

// copyAttrs are the attributes that carry copy.
//
// Everything else — className, style, href, the path data inside an inline SVG
// — is markup, and checking it produces nothing but noise: "btn btn-primary" is
// a class list and "M4.8 7.8a4.5 4.5 0 0 1…" is an icon.
var copyAttrs = regexp.MustCompile(`\s(?:placeholder|title|aria-label|alt)="([^"]*)"`)

// textRun is the text between two tags, on one line.
//
// Single-line on purpose. The templates this check came from were mostly
// markup, so a run spanning newlines was still a run of prose; a TypeScript
// file is mostly code, and a pattern that crossed lines would sweep up
// statements between two distant JSX tags and report them as untranslated
// English.
var textRun = regexp.MustCompile(`>([^<>\n]+)<`)

// literalOK is the text that is the same in every language because it is not
// copy. It is short on purpose: each entry is a decision somebody made, and a
// long list would mean the check had stopped being about translation.
var literalOK = map[string]bool{
	// The API reference shows this header verbatim, in both languages. It names
	// a header and a placeholder; translating it would make it wrong.
	"Authorization: Bearer <token>": true,

	// The copy table is the one thing that cannot be reported through the
	// interface it is missing from, so this fallback is the interface's only
	// hardcoded sentence — and it is hardcoded in both languages for the same
	// reason the language switcher labels each entry in its own language.
	// See I18nProvider in web/src/lib/i18n.tsx.
	"无法加载界面文案 / Could not load the interface copy":                            true,
	"GET /admin/api/i18n 失败。请检查服务是否在运行，然后刷新。":                                 true,
	"The request to GET /admin/api/i18n failed. Check the server and reload.": true,
}

// proseLiterals returns the runs of text that look like a sentence rather than
// markup, a symbol or a technical value.
//
// The bar is deliberately low — twelve characters with a space in them — because
// the failure this looks for is a sentence, and every false positive so far has
// been shorter than that. Symbols (`—`, `/`, `*`), identifiers (`per_second`,
// `hmac`) and URLs are all below it or contain no space.
func proseLiterals(source string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{textRun, copyAttrs} {
		for _, m := range re.FindAllStringSubmatch(source, -1) {
			s := strings.TrimSpace(m[1])
			if len(s) < 12 || !strings.Contains(s, " ") || literalOK[s] {
				continue
			}
			// A run that is entirely punctuation and placeholders is not prose.
			if !strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

// stripTSX removes everything in a TypeScript file that is not literal markup:
// comments, and every `{...}` expression.
//
// ---------------------------------------------------------------------------
// Depth-tracked rather than matched with a regular expression, because JSX
// expressions nest — `{items.map((i) => <b>{i}</b>)}` is one expression
// containing another — and a pattern that stopped at the first closing brace
// would leave the tail of that line to be read as prose.
//
// Quoted strings are skipped only while inside an expression. Outside one they
// are attribute values, which is exactly what copyAttrs is looking for.
//
// This is a scanner and not a parser, and it is allowed to be: it runs over
// this repository's own sources, and the worst it can do with a file it
// misreads is report a literal that is not there — which is a failing test
// somebody looks at, not a wrong answer nobody notices.
// ---------------------------------------------------------------------------
func stripTSX(source string) string {
	var out strings.Builder
	depth := 0

	for i := 0; i < len(source); {
		switch {
		case strings.HasPrefix(source[i:], "//"):
			if n := strings.IndexByte(source[i:], '\n'); n >= 0 {
				i += n
			} else {
				i = len(source)
			}

		case strings.HasPrefix(source[i:], "/*"):
			if n := strings.Index(source[i+2:], "*/"); n >= 0 {
				i += 2 + n + 2
			} else {
				i = len(source)
			}

		case depth > 0 && (source[i] == '"' || source[i] == '\'' || source[i] == '`'):
			quote := source[i]
			i++
			for i < len(source) && source[i] != quote {
				if source[i] == '\\' {
					i++
				}
				i++
			}
			i++

		case source[i] == '{':
			depth++
			i++

		case source[i] == '}':
			if depth > 0 {
				depth--
			}
			i++

		default:
			if depth == 0 {
				out.WriteByte(source[i])
			}
			i++
		}
	}

	return out.String()
}
