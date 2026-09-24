package channel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ResultClass is the mandatory error classification every Channel must return.
//
// This is the single most important contract in the codebase: it decides
// whether a message is retried, whether it consumes the channel's rate quota,
// and whether the failure counts against the channel's health.
//
// Everything else about a Channel is replaceable; this is not.
//
// The constants are ordered by severity, and ResultClass values are compared
// with > to fold a sequential send's parts into one outcome. The order is
// deliberate:
//
//	ClassNotAttempted < ClassSent < ClassConnectError < ClassTransient < ClassPermanent
//
// ClassNotAttempted sorts lowest because it carries no outcome at all — it
// says the channel was never called. Folding it against a real result must
// yield the real result, or a message whose first part was delivered and whose
// second was held back for quota would be reported as never attempted.
type ResultClass int

const (
	// ClassNotAttempted means the channel was never called.
	//
	// This is not a channel outcome. It is a decision the router made: the
	// channel's breaker is open, its allowance for the window is spent, or the
	// rate limit could not be waited out. The peer was never contacted, so
	// nothing is known about its health, nothing is charged to its quota, and
	// the message has not spent a retry.
	//
	// It is deliberately the zero value. An unset class then means "we did not
	// try", which fails towards waiting rather than towards claiming success.
	//
	// INVARIANT: this class holds if and only if the delivery carries a
	// SkipReason naming which limit was hit. The two are set together in the
	// router and neither is meaningful without the other.
	ClassNotAttempted ResultClass = iota

	// ClassSent means the downstream accepted the message for delivery.
	ClassSent

	// ClassConnectError means we reached for the peer and never got there: DNS
	// failure, connection refused, TLS handshake failure, timeout while
	// dialling. Retryable. Must NOT consume the channel's send quota.
	//
	// Note the difference from ClassNotAttempted, which reads similarly and is
	// not the same: here the channel WAS called and could not be reached, so
	// the failure is evidence about the channel. There, nothing was called and
	// there is no evidence either way.
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
	case ClassNotAttempted:
		return "NOT_ATTEMPTED"
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
	case ClassNotAttempted:
		return "not_attempted"
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
//
// ClassNotAttempted is retryable: the limit that stopped it is temporary by
// construction, and the message is still waiting for its first real attempt.
func (c ResultClass) Retryable() bool {
	return c == ClassNotAttempted || c == ClassConnectError || c == ClassTransient
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

// NotAttempted builds a "the channel was never called" result.
//
// Only the router produces this. A Channel implementation that returns it is
// claiming it did not run, which is a contradiction — it is answering.
func NotAttempted(err error, detail string) Result {
	return Result{Class: ClassNotAttempted, Err: err, Detail: detail}
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
		} else if class == ClassSent || class == ClassNotAttempted {
			// A channel reported a failure without classifying it. Treat it as
			// permanent rather than letting an unset class pass as success.
			//
			// ClassNotAttempted belongs here too: it describes a decision made
			// before a call, and no recipient of a call that happened can be in
			// that state. Seeing it here means the channel built the recipient
			// list without classifying an entry.
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
		res.Detail = failureDetail(rs)
	default:
		res.Err = fmt.Errorf("channel: all %d recipients failed (%s)", len(rs), worst)
		res.Detail = failureDetail(rs)
	}
	return res
}

// failureDetail renders what the peers said, one line per refused recipient.
//
// Without it the overall Result says only that something failed and how badly —
// "all 1 recipients failed (PERMANENT)" — while the peer's own words, the SMTP
// reply code or the HTTP status, sit in the per-recipient entries a caller may
// never look at. That is the difference between an operator who can fix their
// relay and one who can only see that it is broken.
//
// The address is part of the line on purpose: with several recipients, "the
// peer refused one of these" is not actionable until you know which.
func failureDetail(rs []Recipient) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Accepted || r.Detail == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(r.Address)
		b.WriteString(": ")
		b.WriteString(r.Detail)
	}
	return b.String()
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
