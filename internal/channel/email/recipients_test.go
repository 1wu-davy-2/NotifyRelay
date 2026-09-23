package email

import (
	"context"
	"testing"

	"notifyrelay/internal/channel"
)

// Where the recipients come from is the whole of this milestone: a channel
// configured with a fixed list is an alert route, and one whose list arrives
// with the request is transactional mail.
func TestSend_RequestRecipientsReplaceTheConfiguredOnes(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
		"to":   []any{"oncall@example.com"},
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{
		Ref:        "mailto://user@example.com?via=tx",
		Recipients: []string{"user@example.com"},
	})
	if res.Class != channel.ClassSent {
		t.Fatalf("Send class = %v (%v), want SENT", res.Class, res.Err)
	}

	got := be.captured()
	if len(got) != 1 {
		t.Fatalf("server received %d messages, want 1", len(got))
	}
	if len(got[0].To) != 1 || got[0].To[0] != "user@example.com" {
		t.Errorf("RCPT TO = %v, want [user@example.com]", got[0].To)
	}

	// Replacement, not addition. Appending would mean the on-call mailbox
	// receives every password-reset link addressed to somebody else — which is
	// both a disclosure and a way to bury a real alert.
	for _, to := range got[0].To {
		if to == "oncall@example.com" {
			t.Error("the configured recipient also received a message addressed to someone else")
		}
	}
}

// Several recipients, one request: each gets its own session, so one failure
// does not take the others with it and no recipient learns who the others were.
func TestSend_EachRequestRecipientGetsItsOwnTransaction(t *testing.T) {
	be := newFakeBackend()
	host, port := startFakeSMTP(t, be)

	ch := newTestChannel(t, map[string]any{
		"host": host, "port": port, "tls": "none",
		"from": "relay@example.com",
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{
		Recipients: []string{"a@example.com", "b@example.com"},
	})
	if res.Class != channel.ClassSent {
		t.Fatalf("Send class = %v (%v), want SENT", res.Class, res.Err)
	}
	if len(res.Recipients) != 2 {
		t.Fatalf("per-recipient results = %d, want 2", len(res.Recipients))
	}

	got := be.captured()
	if len(got) != 2 {
		t.Fatalf("server received %d messages, want one per recipient (2)", len(got))
	}
	for _, m := range got {
		if len(m.To) != 1 {
			t.Errorf("RCPT TO = %v, want a single recipient per transaction", m.To)
		}
	}
}

// Neither source supplied an address. Retrying cannot produce one, so the class
// has to be PERMANENT: a transient here would hold the message for the whole
// retention window and then fail anyway.
func TestSend_NoRecipientsAnywhereIsPermanent(t *testing.T) {
	ch := newTestChannel(t, map[string]any{
		"host": "127.0.0.1", "port": 2525, "tls": "none",
		"from": "relay@example.com",
	})

	res := ch.Send(context.Background(), plainMessage(), channel.Target{Ref: "tx"})
	if res.Class != channel.ClassPermanent {
		t.Fatalf("Send class = %v, want PERMANENT", res.Class)
	}
	if res.Err == nil || res.Detail == "" {
		t.Errorf("the failure should say what is missing: err=%v detail=%q", res.Err, res.Detail)
	}
}
