package channel

import (
	"errors"
	"fmt"
	"time"
)

// ResultClass is the mandatory error classification every Channel must return.
//
// This is the single most important contract in the codebase: it decides
// whether a message is retried, whether it consumes the channel's rate quota,
// and whether the failure counts against the channel's health.
//
// Everything else about a Channel is replaceable; this is not.
type ResultClass int

const (
	// ClassSent means the downstream accepted the message for delivery.
	ClassSent ResultClass = iota

	// ClassConnectError means we never reached the peer: DNS failure,
	// connection refused, TLS handshake failure, timeout while dialling.
	// Retryable. Must NOT consume the channel's send quota.
	ClassConnectError

	// ClassTransient means the peer explicitly asked us to try later
	// (SMTP 4xx, HTTP 429/5xx). Retryable. Consumes quota.
	ClassTransient

	// ClassPermanent means the peer definitively rejected the message
	// (SMTP 5xx, unknown recipient, auth failure). Never retried.
	ClassPermanent
)

func (c ResultClass) String() string {
	switch c {
	case ClassSent:
		return "SENT"
	case ClassConnectError:
		return "CONNECT_ERROR"
	case ClassTransient:
		return "TRANSIENT"
	case ClassPermanent:
		return "PERMANENT"
	default:
		return fmt.Sprintf("ResultClass(%d)", int(c))
	}
}

// Wire returns the lowercase form used in API responses.
//
// String() is the uppercase form used in audit records and logs; keeping the
// two distinct stops a rename of one from silently changing the other.
func (c ResultClass) Wire() string {
	switch c {
	case ClassSent:
		return "sent"
	case ClassConnectError:
		return "connect_error"
	case ClassTransient:
		return "transient"
	case ClassPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// Retryable reports whether a retry could plausibly succeed.
func (c ResultClass) Retryable() bool {
	return c == ClassConnectError || c == ClassTransient
}

// Recipient records the outcome for one addressee of a multi-recipient send.
//
// Only channels that address recipients independently (mail, and any future
// fan-out channel) populate this. Channels with a single endpoint leave
// Result.Recipients nil.
type Recipient struct {
	Address  string
	Accepted bool
	Class    ResultClass // ClassSent when Accepted, otherwise the failure class
	Detail   string      // the peer's own words, for the audit trail
}

// Result is what every Channel.Send and Channel.Test returns.
type Result struct {
	Class      ResultClass
	Err        error
	Detail     string      // peer response summary, safe to log; never a credential
	Elapsed    time.Duration
	Recipients []Recipient // nil unless the channel addresses recipients individually
}

// Sent builds a success result.
func Sent(detail string) Result {
	return Result{Class: ClassSent, Detail: detail}
}

// ConnectError builds a "never reached the peer" result.
func ConnectError(err error, detail string) Result {
	return Result{Class: ClassConnectError, Err: err, Detail: detail}
}

// Transient builds a "peer said try later" result.
func Transient(err error, detail string) Result {
	return Result{Class: ClassTransient, Err: err, Detail: detail}
}

// Permanent builds a "peer definitively rejected" result.
func Permanent(err error, detail string) Result {
	return Result{Class: ClassPermanent, Err: err, Detail: detail}
}

// FromRecipients derives an overall class from per-recipient outcomes.
//
// The overall class is the most severe per-recipient class, using the
// ResultClass ordering Sent < ConnectError < Transient < Permanent.
//
// Taking the maximum rather than flattening every failure to one class
// preserves the distinction the retry layer depends on: if no recipient was
// ever reached, the result stays ConnectError and must not consume the
// channel's send quota. A mixed batch containing a transient failure does
// consume quota, because some peer did answer.
//
// CAUTION for the retry layer: a retryable result on a partial send means a
// retry will re-deliver to the recipients that already succeeded. Until M4
// tracks per-recipient delivery state, prefer NOT retrying partial sends —
// the per-recipient detail is preserved either way so the caller can decide.
func FromRecipients(rs []Recipient) Result {
	if len(rs) == 0 {
		return Permanent(errors.New("channel: no recipients"), "")
	}

	res := Result{Recipients: rs}
	accepted := 0
	worst := ClassSent

	for _, r := range rs {
		class := r.Class
		if r.Accepted {
			class = ClassSent
			accepted++
		} else if class == ClassSent {
			// A channel reported a failure without classifying it. Treat it as
			// permanent rather than letting the zero value pass as success.
			class = ClassPermanent
		}
		if class > worst {
			worst = class
		}
	}

	res.Class = worst
	switch {
	case worst == ClassSent:
		res.Detail = fmt.Sprintf("%d/%d recipients accepted", accepted, len(rs))
	case accepted > 0:
		res.Err = fmt.Errorf("channel: %d of %d recipients accepted, the rest failed (%s)",
			accepted, len(rs), worst)
	default:
		res.Err = fmt.Errorf("channel: all %d recipients failed (%s)", len(rs), worst)
	}
	return res
}

// WithElapsed returns a copy of r with the elapsed duration recorded.
func (r Result) WithElapsed(d time.Duration) Result {
	r.Elapsed = d
	return r
}

// Error implements the error interface so a failed Result can be returned
// directly from helpers that expect an error.
func (r Result) Error() string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return r.Class.String()
}
