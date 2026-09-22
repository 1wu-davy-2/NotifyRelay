package admin

import (
	"fmt"
	"net/http"
	"strings"
)

// The API reference page.
//
// It exists because the person who has to call this service is often not the
// person who deployed it, and the answer to "how do I send one of these from
// Python" should not be "read the Go source". The samples are real files under
// samples/ rather than strings in this file, so they can be edited as code and
// syntax-highlighted by an editor.
//
// They are checked in one specific way: no sample may contain a way to turn
// off certificate verification. The bearer token travels in a header, so an
// unverified connection hands it to whoever is in the middle, and a copy-pasted
// sample is exactly how that mistake propagates. See apidocs_test.go.

// apiDocSample is one language's worked example.
type apiDocSample struct {
	ID    string // anchor and tab identifier
	Label string // what the tab says
	Note  string // one line about the dependency, if any
	Body  string // the code, with the base URL already substituted
}

// apiDocEndpoint is one row of the endpoint table.
type apiDocEndpoint struct {
	Method string
	Path   string
	Auth   string // "Bearer" or "none"
	Zh     string
	En     string
}

// apiDocError is one row of the error-code table.
type apiDocError struct {
	Code   string
	Status string
	Zh     string
	En     string
}

// sampleFiles is the tab order. Fixed rather than sorted: curl first because
// it is what somebody reaches for to check the service is alive, then the
// languages in rough order of how often they turn up asking.
var sampleFiles = []apiDocSample{
	{ID: "curl", Label: "curl", Note: "no dependency"},
	{ID: "go", Label: "Go", Note: "standard library"},
	{ID: "python", Label: "Python", Note: "standard library"},
	{ID: "java", Label: "Java", Note: "11+, java.net.http"},
	{ID: "csharp", Label: "C#", Note: ".NET 5+, System.Net.Http"},
	{ID: "c", Label: "C", Note: "libcurl"},
	{ID: "cpp", Label: "C++", Note: "cpp-httplib, header-only"},
}

// loadSamples reads the embedded samples once, at startup.
//
// A missing or renamed file is a failure to start rather than a page that
// renders with a blank tab. The same reasoning as parsing the templates up
// front: it is much easier to notice a service that will not start than a page
// nobody opened until the day they needed it.
func loadSamples() ([]apiDocSample, error) {
	out := make([]apiDocSample, 0, len(sampleFiles))

	for _, s := range sampleFiles {
		raw, err := assets.ReadFile("samples/" + s.ID + ".txt")
		if err != nil {
			return nil, fmt.Errorf("admin: reading the %s API sample: %w", s.ID, err)
		}
		s.Body = string(raw)
		out = append(out, s)
	}
	return out, nil
}

var apiSamples = func() []apiDocSample {
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

// apiDocsPage implements GET /admin/api-docs.
func (h *handler) apiDocsPage(w http.ResponseWriter, r *http.Request, actor string) {
	lang := r.URL.Query().Get("lang")
	if lang != "en" {
		lang = "zh"
	}

	base := h.pageBase(r, actor, "api-docs")
	// pageBase derives the title from the nav key, which would read "Api-docs".
	base.Title = "API"

	baseURL := apiBaseURL(r)

	samples := make([]apiDocSample, 0, len(apiSamples))
	for _, s := range apiSamples {
		s.Body = strings.ReplaceAll(s.Body, "{{BASE_URL}}", baseURL)
		samples = append(samples, s)
	}

	h.render(w, r, "apidocs.html", struct {
		pageData
		BaseURL   string
		Lang      string
		Zh        bool
		Samples   []apiDocSample
		Endpoints []apiDocEndpoint
		Errors    []apiDocError
		// Secure says whether the operator's own connection to this page is
		// TLS. If it is not, the token they are about to copy into a script
		// will cross the network in the clear, and that is worth saying on the
		// page rather than in a document nobody opens.
		Secure bool
	}{
		pageData:  base,
		BaseURL:   baseURL,
		Lang:      lang,
		Zh:        lang == "zh",
		Samples:   samples,
		Endpoints: apiEndpoints,
		Errors:    apiErrors,
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
