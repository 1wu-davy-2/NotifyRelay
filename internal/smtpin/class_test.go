package smtpin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/config"
	"notifyrelay/internal/message"
	"notifyrelay/internal/router"
)

// The stub is a real Router with one channel that answers a fixed class, rather
// than a hand-built TargetResult: router.TargetResult carries its class in an
// unexported field precisely so that only the router can say what a delivery
// outcome was, and reaching around that would test the test rather than the
// switch under it.
const stubType = "smtptest"

var stubClass channel.ResultClass

type stubChannel struct{}

func (stubChannel) Type() string                    { return stubType }
func (stubChannel) ParamSchema() []channel.ParamSpec { return nil }
func (stubChannel) Capability() channel.Capability {
	return channel.Capability{SupportedFormats: []message.Format{message.FormatText}}
}
func (stubChannel) Send(context.Context, *message.Message) channel.Result {
	return channel.Result{Class: stubClass, Detail: "stub"}
}
func (stubChannel) Test(context.Context) channel.Result { return channel.Sent("stub") }

func init() {
	channel.Register(channel.Descriptor{
		Type: stubType,
		Factory: func(string, map[string]any) (channel.Channel, error) {
			return stubChannel{}, nil
		},
	})
}

const sampleMail = "Subject: t\r\n\r\nbody\r\n"

// deliver drives one message through the SMTP data path and reports the reply
// code, or 0 when the message was accepted.
func deliver(t *testing.T, class channel.ResultClass) int {
	t.Helper()
	stubClass = class

	r, err := router.New(router.Options{
		Channels:       []config.ChannelConfig{{Name: "oncall", Type: stubType}},
		DeliverTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}

	s := &session{
		server: &Server{
			deliverer: r,
			log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
		rcpts: []envelopeRecipient{{address: "oncall@relay.local", alias: "oncall"}},
	}

	err = s.Data(strings.NewReader(sampleMail))
	if err == nil {
		return 0
	}

	var se *smtp.SMTPError
	if !errors.As(err, &se) {
		t.Fatalf("unexpected error type %T: %v", err, err)
	}
	return se.Code
}

// A delivery nobody attempted is a temporary condition, and the SMTP reply has
// to say so.
//
// 451 tells the sending MTA to retry on its own schedule; 550 bounces the mail
// back to whoever sent it. Reporting "permanently failed" for a channel that is
// merely circuit-broken or out of allowance would destroy the notification at
// the one moment the relay exists to protect it — and it is exactly what a
// switch with a bare `default:` branch produces silently when a new class is
// added, which is why this test exists.
func TestData_NotAttemptedIsDeferredNotBounced(t *testing.T) {
	cases := []struct {
		name  string
		class channel.ResultClass
	}{
		{"breaker open, quota exhausted, rate limited", channel.ClassNotAttempted},
		{"never reached the peer", channel.ClassConnectError},
		{"peer said try later", channel.ClassTransient},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := deliver(t, tc.class); code != 451 {
				t.Errorf("code = %d, want 451 so the sender retries", code)
			}
		})
	}
}

// The other half of the contract: a peer that answered and refused is permanent,
// and the sender must stop rather than retry forever.
func TestData_PermanentIsBounced(t *testing.T) {
	if code := deliver(t, channel.ClassPermanent); code != 550 {
		t.Errorf("code = %d, want 550", code)
	}
}

func TestData_SentIsAccepted(t *testing.T) {
	if code := deliver(t, channel.ClassSent); code != 0 {
		t.Errorf("code = %d, want the message accepted", code)
	}
}
