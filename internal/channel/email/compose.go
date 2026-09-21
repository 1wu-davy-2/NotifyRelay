package email

import (
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/wneessen/go-mail"

	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

// buildMsg assembles the MIME message for one recipient.
//
// PROVENANCE
//
// Repository : github.com/kubesphere/notification-manager
// File       : pkg/notify/notifier/email/email.go  (lines 284-328)
// License    : Apache-2.0 — see the NOTICE file.
//
// What is taken from upstream is the ENCODING POLICY, not its bytes:
//
//   - non-ASCII headers are Q-encoded (mime.QEncoding)
//   - every part declares charset=UTF-8
//   - the body is quoted-printable encoded
//   - the plain-text part precedes the HTML part, so a multipart/alternative
//     reader picks the richest part it can render
//
// The MIME bytes themselves are produced by go-mail rather than written by
// hand, and the SMTP client is go-mail's (upstream's is not ported).
//
// Upstream declares multipart/alternative but emits a single part. This
// implementation emits both, which is what gives an HTML notification a
// readable fallback in a text-only client. See docs/02-scope.md section 1.1.
func buildMsg(cfg Config, msg *message.Message, to string) (*mail.Msg, error) {
	m := mail.NewMsg(mail.WithEncoding(mail.EncodingQP))

	if err := setFrom(m, cfg.From); err != nil {
		return nil, err
	}
	if err := m.To(to); err != nil {
		return nil, fmt.Errorf("recipient %q is not a valid address: %w", to, err)
	}

	m.Subject(subjectFor(cfg, msg))

	switch msg.Format {
	case message.FormatHTML:
		m.SetBodyString(mail.TypeTextPlain, render.HTMLToText(msg.Body))
		m.AddAlternativeString(mail.TypeTextHTML, msg.Body)
	default:
		m.SetBodyString(mail.TypeTextPlain, msg.Body)
	}

	m.SetDate()
	m.SetMessageID()

	return m, nil
}

// setFrom accepts both "addr@example.com" and "Display Name <addr@example.com>".
func setFrom(m *mail.Msg, from string) error {
	addr, err := netmail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return fmt.Errorf("invalid \"from\" address %q: %w", from, err)
	}
	if addr.Name != "" {
		return m.FromFormat(addr.Name, addr.Address)
	}
	return m.From(addr.Address)
}

// subjectFor applies the channel's subject template.
//
// Templates are deliberately logic-free placeholder substitution; see
// message.Render. The template is validated at startup, so an unknown
// placeholder here means the caller built a message the config did not expect
// — the placeholder is left visible rather than blanked out.
func subjectFor(cfg Config, msg *message.Message) string {
	if cfg.SubjectTemplate == "" {
		return severityPrefix(msg) + msg.Title
	}
	return severityPrefix(msg) + message.Render(cfg.SubjectTemplate, msg.Vars())
}

// severityPrefix marks the severity of the notification in the subject line.
//
// Mail has no colour or icon to carry severity, so the subject is the only
// place it can show up before the message is opened. Channels with richer
// rendering (IM cards) use colour instead; see M3.
func severityPrefix(msg *message.Message) string {
	switch msg.Type {
	case message.TypeWarning:
		return "[WARN] "
	case message.TypeFailure:
		return "[ALERT] "
	case message.TypeSuccess:
		return "[OK] "
	default:
		return ""
	}
}
