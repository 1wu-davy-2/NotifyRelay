// Package httpauth applies credentials and signatures to outbound HTTP requests.
//
// Authentication is separated from channel logic on purpose. Signing schemes —
// an HMAC over the body, a token in a header, a credential that has to be
// refreshed — are cross-cutting concerns. Left inside channel implementations
// they get reimplemented slightly differently every time, which is how one
// channel ends up logging a secret that another correctly redacts.
package httpauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"notifyrelay/internal/channel"
)

// Authenticator applies an authentication scheme to an outbound request.
//
// The body is passed separately because some schemes sign the payload rather
// than the request line, and reading it back from the request is not possible
// once the body has been consumed.
type Authenticator interface {
	Apply(req *http.Request, body []byte) error
	// Describe names the scheme for logs and the /channels endpoint. It must
	// never include the credential itself.
	Describe() string
}

// ------------------------------------------------------------------ schemes

type none struct{}

// None applies no authentication.
func None() Authenticator { return none{} }

func (none) Apply(*http.Request, []byte) error { return nil }
func (none) Describe() string                  { return "none" }

type bearer struct{ token string }

// Bearer sets an Authorization: Bearer header.
func Bearer(token string) Authenticator { return bearer{token: token} }

func (b bearer) Apply(req *http.Request, _ []byte) error {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return nil
}
func (bearer) Describe() string { return "bearer" }

type basic struct{ username, password string }

// Basic sets HTTP Basic credentials.
func Basic(username, password string) Authenticator {
	return basic{username: username, password: password}
}

func (b basic) Apply(req *http.Request, _ []byte) error {
	req.SetBasicAuth(b.username, b.password)
	return nil
}
func (basic) Describe() string { return "basic" }

type headerValue struct{ name, value string }

// HeaderValue sets a fixed header, for schemes that are neither bearer nor basic.
func HeaderValue(name, value string) Authenticator {
	return headerValue{name: name, value: value}
}

func (h headerValue) Apply(req *http.Request, _ []byte) error {
	req.Header.Set(h.name, h.value)
	return nil
}
func (h headerValue) Describe() string { return "header " + h.name }

type hmacSigner struct {
	secret   string
	header   string
	prefix   string
	encoding string // "hex" or "base64"
}

// HMACSigner signs the request body with HMAC-SHA256 and puts the digest in a
// header, optionally prefixed (for example "sha256=").
//
// Signing the body is what lets a receiver prove the payload arrived unmodified
// and from someone holding the shared secret — the cheapest meaningful
// authentication for a webhook receiver.
func HMACSigner(secret, header, prefix, encoding string) Authenticator {
	return hmacSigner{secret: secret, header: header, prefix: prefix, encoding: encoding}
}

func (h hmacSigner) Apply(req *http.Request, body []byte) error {
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	sum := mac.Sum(nil)

	var encoded string
	if h.encoding == "base64" {
		encoded = base64.StdEncoding.EncodeToString(sum)
	} else {
		encoded = hex.EncodeToString(sum)
	}

	req.Header.Set(h.header, h.prefix+encoded)
	return nil
}
func (h hmacSigner) Describe() string { return "hmac-sha256 " + h.header }

// ------------------------------------------------------------------- config

// Params holds the credential fields a channel reads from its configuration.
type Params struct {
	Type            string // none | bearer | basic | header | hmac
	Token           string
	Username        string
	Password        string
	HeaderName      string
	HeaderValue     string
	Secret          string
	SignatureHeader string
	SignaturePrefix string
	SignatureBase64 bool
}

// ParamSpecs returns the schema entries for the fields Params reads.
//
// Channels embed this in their own schema so every channel that can
// authenticate declares the same parameter names and the same documentation.
//
// The ShowIf on each credential is what makes the set usable as a form. Ten
// parameters of which at most three apply at a time, with none of them marked
// required, is a form that cannot tell an operator what it wants — and
// FromParams below enforces exactly the same shape at startup, so the two
// disagreeing is what an operator would experience as "the form let me save
// something that will not start".
func ParamSpecs() []channel.ParamSpec {
	when := func(mode string) *channel.Condition {
		return &channel.Condition{Field: "auth_type", Equals: mode}
	}

	return []channel.ParamSpec{
		{
			Name: "auth_type", Type: channel.ParamEnum, Default: "none",
			Values: []string{"none", "bearer", "basic", "header", "hmac"},
			Label:  "Authentication", Desc: "How to authenticate to the endpoint.",
		},
		{
			Name: "token", Type: channel.ParamString, Private: true, ShowIf: when("bearer"),
			Label: "Bearer token", Desc: "Used when auth_type is bearer. Write as `!env ...`.",
		},
		{
			Name: "username", Type: channel.ParamString, ShowIf: when("basic"),
			Label: "Username", Desc: "Used when auth_type is basic.",
		},
		{
			Name: "password", Type: channel.ParamString, Private: true, ShowIf: when("basic"),
			Label: "Password", Desc: "Used when auth_type is basic. Write as `!env ...`.",
		},
		{
			Name: "header_name", Type: channel.ParamString, ShowIf: when("header"),
			Label: "Header name", Desc: "Used when auth_type is header.",
		},
		{
			Name: "header_value", Type: channel.ParamString, Private: true, ShowIf: when("header"),
			Label: "Header value", Desc: "Used when auth_type is header. Write as `!env ...`.",
		},
		{
			Name: "secret", Type: channel.ParamString, Private: true, ShowIf: when("hmac"),
			Label: "Signing secret", Desc: "Used when auth_type is hmac. Write as `!env ...`.",
		},
		{
			Name: "signature_header", Type: channel.ParamString, Default: "X-Signature", ShowIf: when("hmac"),
			Label: "Signature header", Desc: "Used when auth_type is hmac.",
		},
		{
			Name: "signature_prefix", Type: channel.ParamString, ShowIf: when("hmac"),
			Label: "Signature prefix", Desc: "Prepended to the digest, for example \"sha256=\".",
		},
		{
			Name: "signature_base64", Type: channel.ParamBool, ShowIf: when("hmac"),
			Label: "Base64 signature", Desc: "Emit the digest as base64 instead of hex.",
		},
	}
}

// FromParams builds the authenticator described by the configuration.
func FromParams(p Params) (Authenticator, error) {
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "", "none":
		return None(), nil

	case "bearer":
		if p.Token == "" {
			return nil, fmt.Errorf("auth_type \"bearer\" requires \"token\"")
		}
		return Bearer(p.Token), nil

	case "basic":
		if p.Username == "" {
			return nil, fmt.Errorf("auth_type \"basic\" requires \"username\"")
		}
		return Basic(p.Username, p.Password), nil

	case "header":
		if p.HeaderName == "" || p.HeaderValue == "" {
			return nil, fmt.Errorf("auth_type \"header\" requires \"header_name\" and \"header_value\"")
		}
		return HeaderValue(p.HeaderName, p.HeaderValue), nil

	case "hmac":
		if p.Secret == "" {
			return nil, fmt.Errorf("auth_type \"hmac\" requires \"secret\"")
		}
		header := p.SignatureHeader
		if header == "" {
			header = "X-Signature"
		}
		encoding := "hex"
		if p.SignatureBase64 {
			encoding = "base64"
		}
		return HMACSigner(p.Secret, header, p.SignaturePrefix, encoding), nil

	default:
		return nil, fmt.Errorf("auth_type %q is not one of none, bearer, basic, header, hmac", p.Type)
	}
}
