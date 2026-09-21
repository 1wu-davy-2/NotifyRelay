// Package httpx posts notifications to HTTP endpoints and classifies the
// outcome.
//
// Every HTTP-based channel faces the same three questions — how long to wait,
// how to turn a status code into one of the three result classes, and how much
// of the response to keep for the audit trail. Answering them once here keeps
// the answers from drifting apart between channels, which is how one channel
// ends up retrying a 404 forever.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/channel/httpauth"
)

const (
	// detailLimit caps how much of a response body reaches the audit trail.
	// Enough to diagnose, short enough not to paste a whole HTML error page.
	detailLimit = 240
	// maxResponseBytes caps what is read from a response at all.
	maxResponseBytes = 64 << 10
)

// Client sends notifications to HTTP endpoints.
type Client struct {
	http *http.Client
}

// ParamSpec is the schema entry for the optional trusted-root bundle.
//
// Channels add it to their own schema. It exists because a corporate proxy
// that inspects TLS presents its own certificate, and rejecting it would make
// the service unusable on exactly the networks it is deployed on. There is
// deliberately no "skip verification" option: silently accepting any
// certificate is how a relay hands its credentials to whatever answers.
func ParamSpec() channel.ParamSpec {
	return channel.ParamSpec{
		Name: "ca_file", Type: channel.ParamString,
		Label: "CA bundle",
		Desc:  "PEM file of extra trusted roots, for a private CA or a TLS-inspecting proxy.",
	}
}

// NewClientFromConfig builds a client, trusting extra roots when caFile is set.
//
// The file is read once at startup so a bad path fails the service at boot
// rather than on the first notification.
func NewClientFromConfig(timeout time.Duration, caFile string) (*Client, error) {
	if caFile == "" {
		return NewClient(timeout, nil), nil
	}

	pool := x509.NewCertPool()
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("parameter \"ca_file\": %w", err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("parameter \"ca_file\": %q contains no PEM certificates", caFile)
	}

	return NewClient(timeout, &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}), nil
}

// NewClient builds a client with an explicit timeout.
//
// A zero timeout would mean "wait forever", which turns one unresponsive
// endpoint into a stuck delivery.
func NewClient(timeout time.Duration, tlsConfig *tls.Config) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// Clone the default transport rather than sharing it: a channel that sets
	// its own TLS roots must not change how every other channel connects.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	return &Client{http: &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}}
}

// Post sends body to url and classifies the response.
func (c *Client) Post(
	ctx context.Context,
	url, contentType string,
	body []byte,
	headers map[string]string,
	auth httpauth.Authenticator,
) channel.Result {
	result, _ := c.PostRaw(ctx, url, contentType, body, headers, auth)
	return result
}

// PostRaw is Post, but also returns the response body.
//
// Some APIs report application errors inside a successful HTTP response —
// Slack answers 200 with {"ok":false,"error":...}. A caller that has to look
// inside the body needs it, and re-reading the response is not possible once
// the body has been consumed.
func (c *Client) PostRaw(
	ctx context.Context,
	url, contentType string,
	body []byte,
	headers map[string]string,
	auth httpauth.Authenticator,
) (channel.Result, []byte) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return channel.Permanent(redactEndpoint(err, url), "the endpoint URL is not usable"), nil
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	if auth != nil {
		if err := auth.Apply(req, body); err != nil {
			return channel.Permanent(fmt.Errorf("apply auth: %w", err),
				"the configured authentication could not be applied"), nil
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return ClassifyError(err, url), nil
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	return ClassifyStatus(resp.StatusCode, raw, resp.Header.Get("Retry-After")), raw
}

// ClassifyStatus maps an HTTP response onto the three-class contract.
//
// The split follows what a retry can actually change:
//
//	2xx                     -> sent
//	408, 425, 429, 5xx      -> transient; the endpoint is asking us to come back
//	any other 4xx           -> permanent; the request itself is wrong
//	3xx                     -> permanent; the client follows redirects, so a
//	                           redirect reaching here means a loop or a broken
//	                           configuration, and neither improves on retry
func ClassifyStatus(status int, body []byte, retryAfter string) channel.Result {
	detail := fmt.Sprintf("HTTP %d", status)
	if retryAfter != "" {
		detail += " retry-after=" + retryAfter
	}
	if summary := summarise(body); summary != "" {
		detail += " " + summary
	}

	switch {
	case status >= 200 && status < 300:
		return channel.Sent(detail)

	case status == http.StatusRequestTimeout,
		status == http.StatusTooEarly,
		status == http.StatusTooManyRequests,
		status >= 500:
		return channel.Transient(fmt.Errorf("endpoint responded %d", status), detail)

	default:
		return channel.Permanent(fmt.Errorf("endpoint responded %d", status), detail)
	}
}

// ClassifyError maps a transport failure onto the three-class contract.
//
// Nothing here reached a peer that gave a verdict, so every case is
// CONNECT_ERROR — retryable, and not charged against the channel's quota.
//
// endpoint is the URL the call was aimed at. It is required rather than
// optional because the error it produces is quoted in the API response and
// written to the audit trail, and for several channels the credential is part
// of that URL. Making the parameter mandatory means a new caller cannot forget
// it and quietly reintroduce the leak.
func ClassifyError(err error, endpoint string) channel.Result {
	if err == nil {
		return channel.Sent("")
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return channel.ConnectError(redactEndpoint(err, endpoint), "never reached the endpoint")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return channel.ConnectError(redactEndpoint(err, endpoint), "the endpoint host could not be resolved")
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return channel.ConnectError(redactEndpoint(err, endpoint), "the endpoint's TLS certificate was not trusted")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return channel.ConnectError(redactEndpoint(err, endpoint), "timed out before the endpoint answered")
	}
	if errors.Is(err, context.Canceled) {
		return channel.ConnectError(redactEndpoint(err, endpoint), "cancelled before the endpoint answered")
	}

	return channel.ConnectError(redactEndpoint(err, endpoint), "the request could not be completed")
}

// endpointLabel reduces a URL to the part that is safe to quote: the scheme and
// the host.
//
// The query is where DingTalk and Feishu put their access token; the path is
// where Slack puts its webhook secret. Both are credentials that happen to look
// like a URL, and the host is the only part an operator needs in order to act
// on the message. Which endpoint failed is answered by "hooks.slack.com"; what
// the secret was is answered by nothing that belongs in a log.
func endpointLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "endpoint"
	}
	return u.Scheme + "://" + u.Host
}

// endpointError is a URL error with the credential-bearing parts removed.
//
// It keeps the underlying cause reachable, so errors.Is(err,
// context.DeadlineExceeded) and the dial/DNS/TLS checks above still work on the
// redacted error — the classification must not be lost along with the secret.
type endpointError struct {
	op       string
	endpoint string
	err      error
}

func (e *endpointError) Error() string { return e.op + " " + e.endpoint + ": " + e.err.Error() }
func (e *endpointError) Unwrap() error { return e.err }

// redactEndpoint strips the path and query from any *url.Error in the chain.
//
// net/http wraps every transport failure in one, and its Error() renders the
// whole URL. An error that is not a *url.Error is returned untouched: it never
// carried the URL in the first place.
func redactEndpoint(err error, raw string) error {
	if err == nil {
		return nil
	}

	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	return &endpointError{op: uerr.Op, endpoint: endpointLabel(raw), err: uerr.Err}
}

// RetryAfterSeconds parses a Retry-After header, returning 0 when absent or
// unparseable. M4 uses it to schedule the next attempt; HTTP-date form is not
// supported because no endpoint this service talks to uses it.
func RetryAfterSeconds(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// summarise flattens and truncates a response body for the audit trail.
func summarise(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) <= detailLimit {
		return s
	}
	return s[:detailLimit] + "..."
}
