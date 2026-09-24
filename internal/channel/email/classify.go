package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	"github.com/wneessen/go-mail"

	"notifyrelay/internal/channel"
)

// classify maps a delivery failure onto the three-class contract.
//
// The rules follow SMTP reality rather than the Go error type:
//
//   - Anything that is not a *mail.SendError means the peer never gave us a
//     verdict — DNS, dial, TLS handshake, timeout. That is CONNECT_ERROR:
//     retryable, and it must NOT consume the channel's send quota.
//   - A *mail.SendError carries the server's reply code. 5xx is a permanent
//     refusal, 4xx is "try later".
//   - A SendError with code 0 was generated locally. A failed connection check
//     is CONNECT_ERROR; a bad sender or recipient list is our own configuration
//     and no retry will fix it.
func classify(err error) channel.Result {
	if err == nil {
		return channel.Sent("")
	}

	// Check the underlying cause before the wrapper. go-mail can return a
	// dial or TLS failure wrapped in a SendError, and the wrapper alone would
	// misreport "never reached the peer" as a transient rejection.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return channel.ConnectError(err, connectDetail(err))
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return channel.ConnectError(err, connectDetail(err))
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return channel.ConnectError(err, "TLS certificate was not trusted")
	}

	var sendErr *mail.SendError
	if !errors.As(err, &sendErr) {
		return channel.ConnectError(err, connectDetail(err))
	}

	// The server's own words are the point of this line.
	//
	// go-mail renders a SendError as "<the step that failed>: <the server's
	// reply>", so this carries both. It used to carry only the first half —
	// Reason.String() names the step and not the refusal — and a 501 from a
	// relay that would not accept the sender was recorded as the bare phrase
	// "sending SMTP MAIL FROM command", with the sentence explaining it sitting
	// unread in the error this function already held.
	//
	// The enhanced status code is no longer appended separately: go-mail parses
	// it out of the reply text in the first place, so it is already in what
	// follows, and repeating it at the end read as a second and different code.
	detail := fmt.Sprintf("smtp code=%d reason=%s", sendErr.ErrorCode(), sendErr.Error())

	if code := sendErr.ErrorCode(); code >= 500 {
		return channel.Permanent(err, detail)
	} else if code >= 400 {
		return channel.Transient(err, detail)
	}

	switch sendErr.Reason {
	case mail.ErrConnCheck:
		return channel.ConnectError(err, detail)
	case mail.ErrGetSender, mail.ErrGetRcpts:
		return channel.Permanent(err, detail)
	default:
		// The transaction was interrupted somewhere between DATA and the
		// final reply. Whether the peer accepted it is unknown, so treat it
		// as retryable rather than pretending it failed for certain.
		return channel.Transient(err, detail)
	}
}

func connectDetail(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out before the peer answered"
	case errors.Is(err, context.Canceled):
		return "cancelled before the peer answered"
	default:
		return "never reached the peer"
	}
}
