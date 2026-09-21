package smtpin

import (
	"strings"
	"testing"

	"notifyrelay/internal/message"
)

func TestParseRecipient(t *testing.T) {
	tests := []struct {
		name      string
		addr      string
		wantAlias string
		wantType  message.Type
		wantErr   bool
	}{
		{name: "plain alias", addr: "oncall@relay.local", wantAlias: "oncall", wantType: message.TypeInfo},
		{name: "explicit failure", addr: "oncall.failure@relay.local", wantAlias: "oncall", wantType: message.TypeFailure},
		{name: "explicit warning", addr: "oncall.warning@relay.local", wantAlias: "oncall", wantType: message.TypeWarning},
		{name: "explicit success", addr: "oncall.success@relay.local", wantAlias: "oncall", wantType: message.TypeSuccess},
		{name: "explicit info", addr: "oncall.info@relay.local", wantAlias: "oncall", wantType: message.TypeInfo},

		// A trailing segment that is not a type belongs to the alias, so a
		// dotted channel name needs no escaping.
		{name: "dotted alias", addr: "ops.team@relay.local", wantAlias: "ops.team", wantType: message.TypeInfo},
		{name: "dotted alias with type", addr: "ops.team.failure@relay.local", wantAlias: "ops.team", wantType: message.TypeFailure},

		{name: "no domain", addr: "oncall", wantAlias: "oncall", wantType: message.TypeInfo},
		{name: "unknown suffix is part of the alias", addr: "a.critical@relay.local", wantAlias: "a.critical", wantType: message.TypeInfo},

		{name: "empty", addr: "", wantErr: true},
		{name: "empty local part", addr: "@relay.local", wantErr: true},
		{name: "only a type", addr: ".failure@relay.local", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alias, typ, err := ParseRecipient(tt.addr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRecipient(%q) = (%q, %q), want an error", tt.addr, alias, typ)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRecipient(%q): %v", tt.addr, err)
			}
			if alias != tt.wantAlias {
				t.Errorf("alias = %q, want %q", alias, tt.wantAlias)
			}
			if typ != tt.wantType {
				t.Errorf("type = %q, want %q", typ, tt.wantType)
			}
		})
	}
}

func TestToMessage_DecodesSubjectAndPrefersHTML(t *testing.T) {
	raw := strings.Join([]string{
		"From: Monitor <monitor@example.com>",
		"To: oncall@relay.local",
		"Subject: =?utf-8?q?=E6=95=B0=E6=8D=AE=E5=BA=93=E5=91=8A=E8=AD=A6?=",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="BOUND"`,
		"",
		"--BOUND",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"replication lag 12s",
		"--BOUND",
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<p>replication lag <b>12s</b></p>",
		"--BOUND--",
		"",
	}, "\r\n")

	msg, err := toMessage([]byte(raw))
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}

	if msg.Title != "数据库告警" {
		t.Errorf("title = %q, want 数据库告警 (RFC 2047 must be decoded)", msg.Title)
	}
	if msg.Format != message.FormatHTML {
		t.Errorf("format = %q, want html (the HTML part wins)", msg.Format)
	}
	if !strings.Contains(msg.Body, "<b>12s</b>") {
		t.Errorf("body lost its HTML: %q", msg.Body)
	}
	if got := msg.Meta["from"]; got != "Monitor <monitor@example.com>" {
		t.Errorf("meta[from] = %v", got)
	}
}

func TestToMessage_PlainTextOnly(t *testing.T) {
	raw := strings.Join([]string{
		"From: cron@example.com",
		"Subject: backup done",
		"",
		"backup finished at 03:00",
	}, "\r\n")

	msg, err := toMessage([]byte(raw))
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	if msg.Format != message.FormatText {
		t.Errorf("format = %q, want text", msg.Format)
	}
	if !strings.Contains(msg.Body, "backup finished") {
		t.Errorf("body = %q", msg.Body)
	}
}

func TestToMessage_EmptySubjectGetsAPlaceholder(t *testing.T) {
	raw := "From: cron@example.com\r\n\r\nsomething happened\r\n"

	msg, err := toMessage([]byte(raw))
	if err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	// The message model requires a title; an untitled notification should
	// still be delivered rather than rejected.
	if msg.Title != "(no subject)" {
		t.Errorf("title = %q, want (no subject)", msg.Title)
	}
	if err := msg.Validate(); err != nil {
		t.Errorf("the synthesised message must be valid: %v", err)
	}
}

func TestToMessage_RejectsAMessageWithoutABody(t *testing.T) {
	if _, err := toMessage([]byte("From: a@b\r\nSubject: x\r\n\r\n\r\n")); err == nil {
		t.Error("expected an error for a message with no readable body")
	}
}
