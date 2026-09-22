package admin

import (
	"fmt"
	"net/http"
	"strings"

	"notifyrelay/internal/admin/i18n"
)

// The API reference page.
//
// It exists because the person who has to call this service is often not the
// person who deployed it, and the answer to "how do I send one of these from
// Python" should not be "read the Go source". The samples are real files under
// samples/<lang>/ rather than strings in this file, so they can be edited as
// code and syntax-highlighted by an editor.
//
// The two languages are the same program with different comments, and a test
// holds them to that: strip the prose from both and what is left must match
// byte for byte. Duplicating a file per language is how the two drift, and the
// drift that matters is the code — somebody fixes a header in one copy and the
// other keeps the old one, and the reader who gets the stale one cannot tell.
//
// They are also checked in one specific way: no sample may contain a way to
// turn off certificate verification. The bearer token travels in a header, so
// an unverified connection hands it to whoever is in the middle, and a
// copy-pasted sample is exactly how that mistake propagates. That check runs
// over every language — a translated sample is a sample somebody will paste,
// and a check that only read the English column is the check a translation
// quietly bypasses. See apidocs_test.go and samples_test.go.

// apiDocSample is one language's worked example.
type apiDocSample struct {
	ID    string // anchor and tab identifier
	Label string // what the tab says
	// Note is the line about the dependency, when that line is a package name
	// rather than prose. The prose ones come from the copy table; see
	// sampleNote.
	Note string
	Body string // the code, with the base URL already substituted
}

// apiDocEndpoint is one row of the endpoint table, in both languages.
//
// The purpose column is row data rather than page copy, so it stays here: each
// sentence belongs to the method and path beside it, and splitting fourteen of
// them into fourteen fields of the copy table would make the table they
// describe harder to read, not easier. One column is picked per request, by
// endpointsIn, and the page renders a single-language slice.
type apiDocEndpoint struct {
	Method string
	Path   string
	Auth   string // "Bearer", or "none" for an endpoint that takes no credential
	Zh     string
	En     string
}

// apiDocRow is one endpoint as the page renders it: one language, already
// chosen, and no second column to pick from.
type apiDocRow struct {
	Method  string
	Path    string
	Auth    string // empty when the endpoint takes no credential
	Purpose string
}

// apiDocError is one row of the error-code table, in both languages. Same
// reasoning as apiDocEndpoint.
type apiDocError struct {
	Code   string
	Status string
	Zh     string
	En     string
}

// apiDocErrorRow is one error as the page renders it.
type apiDocErrorRow struct {
	Code    string
	Status  string
	Meaning string
}

// sampleFiles is the tab order. Fixed rather than sorted: curl first because
// it is what somebody reaches for to check the service is alive, then the
// languages in rough order of how often they turn up asking.
//
// Only the notes that name a package are here. The other three are sentences,
// and a sentence is copy; see sampleNote.
var sampleFiles = []apiDocSample{
	{ID: "curl", Label: "curl"},
	{ID: "go", Label: "Go"},
	{ID: "python", Label: "Python"},
	{ID: "java", Label: "Java", Note: "11+, java.net.http"},
	{ID: "csharp", Label: "C#", Note: ".NET 5+, System.Net.Http"},
	{ID: "c", Label: "C", Note: "libcurl"},
	{ID: "cpp", Label: "C++", Note: "cpp-httplib, header-only"},
}

// sampleNote is the dependency line beside a sample's tab.
//
// Three of the seven are prose and come from the copy table. The remaining four
// name a package — "libcurl", "11+, java.net.http" — and read the same in both
// languages, which is why they are carried on the row and not translated;
// docs/i18n-inventory.md §4 lists them under "deliberately not translated".
//
// Python reads the Go field because the note is the same sentence, not because
// the two samples have anything else in common.
func sampleNote(t *i18n.Messages, s apiDocSample) string {
	switch s.ID {
	case "curl":
		return t.APIDocsSampleNoteCurl
	case "go", "python":
		return t.APIDocsSampleNoteGo
	default:
		return s.Note
	}
}

// loadSamples reads the embedded samples once, at startup, for every language.
//
// A missing or renamed file is a failure to start rather than a page that
// renders with a blank tab. The same reasoning as parsing the templates up
// front: it is much easier to notice a service that will not start than a page
// nobody opened until the day they needed it.
//
// Every language must have every sample, and a language missing one is that
// same startup failure rather than a tab that quietly shows another language.
//
// The two copies of a sample are the same program with different comments.
// Duplicating a file per language is how the two drift, so a test strips the
// comments from both and asserts what is left is identical — see
// samples_test.go. The comments are the translation; the code is not.
func loadSamples() (map[i18n.Lang][]apiDocSample, error) {
	out := make(map[i18n.Lang][]apiDocSample, len(i18n.Langs))

	for _, l := range i18n.Langs {
		samples := make([]apiDocSample, 0, len(sampleFiles))
		for _, s := range sampleFiles {
			raw, err := assets.ReadFile("samples/" + string(l) + "/" + s.ID + ".txt")
			if err != nil {
				return nil, fmt.Errorf("admin: reading the %s API sample in %s: %w", s.ID, l, err)
			}
			s.Body = string(raw)
			samples = append(samples, s)
		}
		out[l] = samples
	}
	return out, nil
}

var apiSamples = func() map[i18n.Lang][]apiDocSample {
	s, err := loadSamples()
	if err != nil {
		panic(err)
	}
	return s
}()

// apiEndpoints is the public surface. The operator API under /admin is
// deliberately not listed: it is not a caller's interface, and it is documented
// for operators in docs/07-api.md.
var apiEndpoints = []apiDocEndpoint{
	{"POST", "/api/v1/notify", "Bearer",
		"发一条通知。默认异步：入队即返回 202 与投递 ID",
		"Send a notification. Asynchronous by default: queued, answered 202 with delivery ids"},
	{"GET", "/api/v1/channels", "Bearer",
		"列出每个已注册通道类型的参数 schema 与能力，足以写出合法配置",
		"Every registered channel type with its parameter schema and capabilities"},
	{"GET", "/api/v1/messages", "Bearer",
		"列出投递。筛选参数 status / target / request_id / limit / offset",
		"List deliveries. Filters: status, target, request_id, limit, offset"},
	{"GET", "/api/v1/messages/{id}", "Bearer",
		"单条投递，含完整尝试历史（分类、skip_reason、耗时）",
		"One delivery with its full attempt history: class, skip_reason, elapsed"},
	{"GET", "/healthz", "none",
		"存活探针。不查数据库",
		"Liveness. Does not touch the database"},
	{"GET", "/readyz", "none",
		"就绪探针。查数据库",
		"Readiness. Does check the database"},
	{"GET", "/metrics", "none",
		"Prometheus 指标。不需要鉴权",
		"Prometheus metrics. Unauthenticated"},
}

var apiErrors = []apiDocError{
	{"unauthorized", "401", "Bearer 缺失或无效", "Missing or invalid bearer token"},
	{"invalid_request", "400", "请求体或查询参数不合法", "Malformed body or query parameter"},
	{"unknown_target", "400", "目标别名解析不出来（仅异步路径）", "A target does not resolve (async path only)"},
	{"not_found", "404", "没有这个投递 ID", "No delivery with that id"},
	{"queue_unavailable", "503", "入队失败", "The delivery could not be queued"},
	{"not_ready", "503", "存储不可达", "The delivery store is not reachable"},
	{"internal", "500", "其余一切。消息里不含内部细节", "Everything else. The message leaks no internals"},
}

// endpointsIn picks one language's column out of the endpoint table.
func endpointsIn(l i18n.Lang) []apiDocRow {
	out := make([]apiDocRow, 0, len(apiEndpoints))
	for _, e := range apiEndpoints {
		row := apiDocRow{Method: e.Method, Path: e.Path, Purpose: e.Zh}
		if l == i18n.EN {
			row.Purpose = e.En
		}
		// "none" is the table's marker for an endpoint that takes no
		// credential. The page shows the copy table's word for that, so the
		// row leaves Auth empty and the template branches on it.
		if e.Auth != "none" {
			row.Auth = e.Auth
		}
		out = append(out, row)
	}
	return out
}

// errorsIn picks one language's column out of the error-code table.
func errorsIn(l i18n.Lang) []apiDocErrorRow {
	out := make([]apiDocErrorRow, 0, len(apiErrors))
	for _, e := range apiErrors {
		row := apiDocErrorRow{Code: e.Code, Status: e.Status, Meaning: e.Zh}
		if l == i18n.EN {
			row.Meaning = e.En
		}
		out = append(out, row)
	}
	return out
}

// apiDocsPage implements GET /admin/api-docs.
func (h *handler) apiDocsPage(w http.ResponseWriter, r *http.Request, actor string) {
	base := h.pageBase(r, actor, "api-docs")

	// The table and the language come from pageBase's single resolution, so the
	// rows and the shell around them cannot disagree about which language this
	// is. The page used to resolve the query parameter a second time, which
	// also meant the cookie was ignored here and nowhere else.
	t, lang := base.T, base.Lang

	baseURL := apiBaseURL(r)

	samples := make([]apiDocSample, 0, len(sampleFiles))
	for _, s := range apiSamples[lang] {
		s.Body = strings.ReplaceAll(s.Body, "{{BASE_URL}}", baseURL)
		s.Note = sampleNote(t, s)
		samples = append(samples, s)
	}

	h.render(w, r, "apidocs.html", struct {
		pageData
		BaseURL string
		Samples []apiDocSample
		// The two tables, one language's column already selected.
		Endpoints []apiDocRow
		Errors    []apiDocErrorRow
		// Secure says whether the operator's own connection to this page is
		// TLS. If it is not, the token they are about to copy into a script
		// will cross the network in the clear, and that is worth saying on the
		// page rather than in a document nobody opens.
		Secure bool
	}{
		pageData:  base,
		BaseURL:   baseURL,
		Samples:   samples,
		Endpoints: endpointsIn(lang),
		Errors:    errorsIn(lang),
		Secure:    cookieSecure(r),
	})
}

// apiBaseURL is the address a caller should use, as seen from this request.
//
// Taken from the request rather than from configuration on purpose: an operator
// reading this page is already reaching the service at the address that works,
// and a configured value would be one more thing that can disagree with
// reality. It is the address *this* operator used, which is not necessarily the
// one a producer should use — behind a proxy, or from another network, it will
// differ, and the page says so.
func apiBaseURL(r *http.Request) string {
	scheme := "http"
	if cookieSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
