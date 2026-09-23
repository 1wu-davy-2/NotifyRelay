package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"notifyrelay/internal/admin/i18n"
)

// docsFor fetches the reference with a chosen Host header and language.
//
// The Host is a parameter because the base URL the samples are written against
// comes from the request, so a test that did not vary it could not see the
// substitution happen. The language is a query parameter for the same reason it
// is everywhere else: it is the source that beats the others, so these tests do
// not depend on each other's cookies.
func (h *harness) docsFor(t *testing.T, host string, lang i18n.Lang, cookie *http.Cookie, forwardedProto string) apiDocsJSONResponse {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/api-docs?lang="+string(lang), nil)
	req.Host = host
	if forwardedProto != "" {
		req.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/api-docs: status %d: %s", rec.Code, rec.Body.String())
	}

	var out apiDocsJSONResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// The reference exists so a caller does not have to read the Go source. Every
// sample file has to arrive, with something in it — a tab that opens onto an
// empty panel is worse than no tab, because it looks like the sample was meant
// to be there.
func TestAPIDocs_EveryAdvertisedLanguageHasASample(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	got := h.docsFor(t, "relay.internal:8080", i18n.EN, cookie, "")

	if len(got.Samples) != len(sampleFiles) {
		t.Errorf("the reference carries %d samples, want %d", len(got.Samples), len(sampleFiles))
	}

	seen := map[string]bool{}
	for _, s := range got.Samples {
		seen[s.ID] = true
		// The samples are embedded, so a renamed file fails at startup rather
		// than here — but a sample that lost its content would not, and these
		// are the checks that notice.
		if len(s.Body) < 400 {
			t.Errorf("%s: the sample is %d bytes, which is too short to be a worked example",
				s.ID, len(s.Body))
		}
		if !strings.Contains(s.Body, "/api/v1/notify") {
			t.Errorf("%s: the sample never calls /api/v1/notify", s.ID)
		}
		if !strings.Contains(s.Body, "Authorization") {
			t.Errorf("%s: the sample never sets the Authorization header", s.ID)
		}
		// A reader who copies {{BASE_URL}} into a script gets a parse error, not
		// a request.
		if strings.Contains(s.Body, "{{BASE_URL}}") {
			t.Errorf("%s: the {{BASE_URL}} placeholder was not substituted", s.ID)
		}
	}

	for _, s := range sampleFiles {
		if !seen[s.ID] {
			t.Errorf("%s is a sample file and no sample was served for it", s.ID)
		}
	}
}

// The address has to come from the request. A configured value would be one
// more thing that can disagree with the address the operator is demonstrably
// reaching the service on.
func TestAPIDocs_BaseURLComesFromTheRequest(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	for _, host := range []string{"relay.internal:8080", "10.1.2.3:9999"} {
		got := h.docsFor(t, host, i18n.EN, cookie, "")
		if want := "http://" + host; got.BaseURL != want {
			t.Errorf("host %q: base_url = %q, want %q", host, got.BaseURL, want)
		}
	}

	// And it has to reach the samples, not just the header block — the whole
	// point is that they are copy-pasteable.
	got := h.docsFor(t, "relay.example:8443", i18n.EN, cookie, "")
	var substituted int
	for _, s := range got.Samples {
		if strings.Contains(s.Body, "http://relay.example:8443") {
			substituted++
		}
	}
	if substituted != len(sampleFiles) {
		t.Errorf("%d of %d samples carry the request's base URL", substituted, len(sampleFiles))
	}
}

// A connection over plain HTTP is about to hand the reader a token to put in a
// script, and the reference says so.
//
// The warning itself is a sentence in the copy table, rendered by the client —
// so what the server owes is the fact it is a sentence about, and that is what
// is asserted here. The rule is the one cookieSecure applies everywhere else:
// the request's own TLS, or a proxy that terminated it and said so.
func TestAPIDocs_ReportsWhetherTheConnectionIsTLS(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	if got := h.docsFor(t, "relay.internal:8080", i18n.EN, cookie, ""); got.Secure {
		t.Error("a request over plain HTTP was reported as secure; the reference would not " +
			"warn that the token crosses in the clear")
	}

	// Behind a TLS-terminating proxy the operator's connection *is* encrypted,
	// and warning about it anyway would be the kind of false alarm that gets
	// warnings ignored.
	got := h.docsFor(t, "relay.example", i18n.EN, cookie, "https")
	if !got.Secure {
		t.Error("X-Forwarded-Proto: https was not read as a secure connection")
	}
	if want := "https://relay.example"; got.BaseURL != want {
		t.Errorf("base_url = %q, want %q behind a TLS-terminating proxy", got.BaseURL, want)
	}
}

// The prose is bilingual; the code is not, because code is code. Switching
// languages must not change which samples are offered.
//
// The sample *bodies* are the interesting half: each is a worked example with
// its comments translated, so a language switch changes the prose inside the
// code and nothing else. Asserting only that the id list is unchanged would
// pass on a build where the Chinese column served the English files.
func TestAPIDocs_LanguageSwitchesTheProseOnly(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	zh := h.docsFor(t, "relay.internal:8080", i18n.ZH, cookie, "")
	en := h.docsFor(t, "relay.internal:8080", i18n.EN, cookie, "")

	if len(zh.Samples) != len(en.Samples) {
		t.Fatalf("zh has %d samples and en has %d", len(zh.Samples), len(en.Samples))
	}
	for i := range zh.Samples {
		if zh.Samples[i].ID != en.Samples[i].ID {
			t.Errorf("sample %d: zh is %q and en is %q; the switch changed which samples "+
				"are offered", i, zh.Samples[i].ID, en.Samples[i].ID)
		}
	}

	// The error-code table is prose and is translated.
	if zh.Errors[0].Meaning == en.Errors[0].Meaning {
		t.Error("the error-code meanings are identical in both languages")
	}
	// The codes themselves are not: a client branches on the string, so
	// translating one would break every caller.
	for i := range zh.Errors {
		if zh.Errors[i].Code != en.Errors[i].Code {
			t.Errorf("error %d: the code is translated (%q vs %q); clients branch on it",
				i, zh.Errors[i].Code, en.Errors[i].Code)
		}
	}
}

// The red line from docs/02-scope.md, enforced on the samples rather than
// trusted to review.
//
// These are the samples a stranger copies into their own service. A sample that
// turns off certificate verification teaches the mistake to everyone who
// pastes it, and the credential here is a bearer token in a header — an
// unverified connection hands it to whoever is on the path.
func TestAPIDocsPage_NoSampleShowsHowToDisableTLSVerification(t *testing.T) {
	// Every one of these appears in a comment explaining why it must not be
	// used, which is the point: a check that cannot tell code from prose is a
	// check that gets deleted the second time it cries wolf. Same reasoning as
	// directivesOnly in deploy/deploy_test.go.
	forbidden := []struct {
		lang   string
		needle string
	}{
		{"python", "verify=False"},
		{"python", "verify = False"},
		{"python", "_create_unverified_context"},
		{"python", "CERT_NONE"},
		{"go", "InsecureSkipVerify"},
		{"csharp", "ServerCertificateCustomValidationCallback"},
		{"csharp", "ServerCertificateValidationCallback"},
		{"cpp", "enable_server_certificate_verification(false)"},
		{"java", "checkServerTrusted"},
		{"java", "TrustAllStrategy"},
		{"c", "CURLOPT_SSL_VERIFYPEER, 0"},
		{"c", "CURLOPT_SSL_VERIFYHOST, 0"},
	}

	// Every language, not just the one the page defaults to. A sample added in
	// a translation is a sample somebody will paste, and a check that only
	// looked at the English column would be the check a translated sample
	// quietly bypasses.
	for _, l := range i18n.Langs {
		for _, s := range apiSamples[l] {
			code := stripCommentsAndStrings(s.Body)

			for _, f := range forbidden {
				if strings.Contains(code, f.needle) {
					t.Errorf("%s/%s sample: contains %q outside a comment — a copied sample "+
						"would send the bearer token over an unverified connection",
						l, s.ID, f.needle)
				}
			}

			// curl's shorthand is a bare -k or --insecure.
			if s.ID == "curl" {
				if regexp.MustCompile(`(^|\s)(-k|--insecure)(\s|$)`).MatchString(code) {
					t.Errorf("%s/curl sample: uses -k/--insecure", l)
				}
			}
		}
	}
}

// The inverse of the check above, and the one that keeps it honest: if the
// stripper were eating everything, the test above would pass on a sample that
// really did disable verification. This asserts a known-bad sample is caught.
func TestStripCommentsAndStrings_StillCatchesRealCode(t *testing.T) {
	for _, bad := range []struct{ src, needle string }{
		{"ctx = ssl._create_unverified_context()\n", "_create_unverified_context"},
		{"resp = requests.post(url, verify=False)\n", "verify=False"},
		{"tr := &http.Transport{InsecureSkipVerify: true}\n", "InsecureSkipVerify"},
		{"curl_easy_setopt(c, CURLOPT_SSL_VERIFYPEER, 0L);\n", "CURLOPT_SSL_VERIFYPEER, 0"},
		{"code := `-k`\n", "-k"},
	} {
		if got := stripCommentsAndStrings(bad.src); !strings.Contains(got, bad.needle) {
			t.Errorf("the stripper removed real code: %q -> %q (lost %q)",
				bad.src, got, bad.needle)
		}
	}

	// And that it does remove the prose, which is the whole reason it exists.
	prose := "# do not use verify=False here\n// nor InsecureSkipVerify\n"
	if got := stripCommentsAndStrings(prose); strings.Contains(got, "verify") || strings.Contains(got, "Insecure") {
		t.Errorf("the stripper kept a comment: %q", got)
	}
}

// blockComment matches /* ... */ including newlines.
var blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// docString matches Python's triple-quoted strings, which are prose in every
// way that matters here but are not comments.
var docString = regexp.MustCompile(`(?s)""".*?"""`)

// stripCommentsAndStrings removes the prose from a sample, leaving what a
// reader would actually execute.
//
// Deliberately crude — it strips comments and docstrings, not string literals
// in general, because the point is to separate "explaining not to do this" from
// "doing this", and every sample explains in a comment or a docstring.
func stripCommentsAndStrings(src string) string {
	src = blockComment.ReplaceAllString(src, "")
	src = docString.ReplaceAllString(src, "")

	var out []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A trailing comment on a line of code: keep the code, drop the rest.
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// The endpoint table is the quick reference; a row silently dropped from it is
// a caller who never learns the endpoint exists.
//
// The absence at the end is the load-bearing half. The operator API is not a
// caller's interface, it is the interface this reference is rendered inside,
// and documenting it here would advertise routes that answer 401 to the bearer
// key every reader of this page is holding.
func TestAPIDocs_ListsThePublicSurface(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	got := h.docsFor(t, "relay.internal:8080", i18n.EN, cookie, "")

	listed := map[string]bool{}
	for _, e := range got.Endpoints {
		listed[e.Method+" "+e.Path] = true
	}

	for _, want := range []string{
		"POST /api/v1/notify",
		"GET /api/v1/channels",
		"GET /api/v1/messages",
		"GET /api/v1/messages/{id}",
		"GET /healthz",
		"GET /readyz",
		"GET /metrics",
	} {
		if !listed[want] {
			t.Errorf("the endpoint table does not list %q", want)
		}
	}

	// The error codes are what a client branches on, so the table has to carry
	// the ones that decide behaviour.
	codes := map[string]bool{}
	for _, e := range got.Errors {
		codes[e.Code] = true
	}
	for _, code := range []string{
		"unauthorized", "invalid_request", "unknown_target", "not_found", "queue_unavailable",
	} {
		if !codes[code] {
			t.Errorf("the error table does not mention %q", code)
		}
	}

	for _, e := range got.Endpoints {
		if strings.HasPrefix(e.Path, "/admin/") {
			t.Errorf("the reference documents %s, which is the operator API and not a "+
				"caller's interface", e.Path)
		}
	}
}
