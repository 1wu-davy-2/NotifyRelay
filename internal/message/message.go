// Package message defines the channel-agnostic message model.
//
// The upstream caller describes WHAT happened (title, body, severity).
// The core router decides HOW it is delivered. Channel implementations
// never see a raw upstream payload — only a Message.
package message

import (
	"errors"
	"fmt"
	"strings"
)

// Format is the markup format of Body, as declared by the caller.
//
// The caller declares it; the service does not guess. The core downgrades
// it to whatever the target channel supports (see internal/router).
type Format string

const (
	FormatText     Format = "text"
	FormatMarkdown Format = "markdown"
	FormatHTML     Format = "html"
)

// ParseFormat parses a caller-supplied format string. Empty means text.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimSpace(s))); f {
	case "", FormatText:
		return FormatText, nil
	case FormatMarkdown:
		return FormatMarkdown, nil
	case FormatHTML:
		return FormatHTML, nil
	default:
		return "", fmt.Errorf("message: unknown format %q (want text, markdown or html)", s)
	}
}

// Type is the semantic severity of the notification. Channels degrade it to
// their own visual vocabulary (mail subject prefix, IM emoji or card colour).
type Type string

const (
	TypeInfo    Type = "info"
	TypeSuccess Type = "success"
	TypeWarning Type = "warning"
	TypeFailure Type = "failure"
)

// ParseType parses a caller-supplied type string. Empty means info.
func ParseType(s string) (Type, error) {
	switch t := Type(strings.ToLower(strings.TrimSpace(s))); t {
	case "", TypeInfo:
		return TypeInfo, nil
	case TypeSuccess, TypeWarning, TypeFailure:
		return t, nil
	default:
		return "", fmt.Errorf("message: unknown type %q (want info, success, warning or failure)", s)
	}
}

// Priority is a 1-5 scale where 1 is lowest and 5 is highest.
//
// It is a single scale shared by every channel so that routing filters and
// channel visual mappings do not each invent their own range.
type Priority int

const (
	PriorityMin     Priority = 1
	PriorityDefault Priority = 3
	PriorityMax     Priority = 5
)

// Valid reports whether p is inside the supported range.
func (p Priority) Valid() bool { return p >= PriorityMin && p <= PriorityMax }

// Link is a structured hyperlink rendered natively where the channel supports it.
type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// Attachment is optional binary content. Only channels that declare
// Capability.SupportAttachment will ever receive one.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"-"`
}

// Message is the single message shape the whole service speaks.
//
// Keep this deliberately small. Fields earn their place by being renderable
// by at least two channels; anything channel-specific belongs in Meta.
// The JSON tags are the spool format: a queued delivery is written to disk as
// JSON and read back by the worker, so the tags are a persistence contract,
// not just a wire format. Renaming a field changes the on-disk shape.
type Message struct {
	Title    string         `json:"title"`
	Body     string         `json:"body"`
	Format   Format         `json:"format"`
	Type     Type           `json:"type"`
	Priority Priority       `json:"priority"`
	Tags     []string       `json:"tags,omitempty"`
	Links    []Link         `json:"links,omitempty"`
	At       []string       `json:"at,omitempty"` // @-mentions, rendered by IM channels only
	Attach   []Attachment   `json:"attach,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"` // channel-private extension point; only the target channel reads it
}

// ErrEmptyBody is returned when a message carries no content at all.
var ErrEmptyBody = errors.New("message: body must not be empty")

// Validate reports whether the message is sendable.
//
// Both Title and Body are required: a notification without a subject is
// unrenderable in mail and in every IM channel. The SMTP inbound path
// synthesises a title when the incoming Subject header is empty.
func (m *Message) Validate() error {
	if strings.TrimSpace(m.Title) == "" {
		return errors.New("message: title must not be empty")
	}
	if strings.TrimSpace(m.Body) == "" {
		return ErrEmptyBody
	}
	if m.Format == "" {
		return errors.New("message: format must be set (use Normalize)")
	}
	if m.Type == "" {
		return errors.New("message: type must be set (use Normalize)")
	}
	if m.Priority != 0 && !m.Priority.Valid() {
		return fmt.Errorf("message: priority %d out of range [%d,%d]", m.Priority, PriorityMin, PriorityMax)
	}
	return nil
}

// Normalize fills in defaults for omitted optional fields.
//
// Call this on messages built from untrusted input (HTTP body, SMTP session)
// before Validate.
func (m *Message) Normalize() {
	if m.Format == "" {
		m.Format = FormatText
	}
	if m.Type == "" {
		m.Type = TypeInfo
	}
	if m.Priority == 0 {
		m.Priority = PriorityDefault
	}
}
