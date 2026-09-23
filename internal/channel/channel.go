// Package channel defines the contract every notification channel implements.
//
// Design rule: the abstraction boundary is the channel TYPE (code), not the
// channel INSTANCE (configuration). Adding a channel type must never require
// touching the router; adding a channel instance must never require touching
// code at all.
package channel

import (
	"context"

	"notifyrelay/internal/message"
	"notifyrelay/internal/render"
)

// ParamType is the declared type of a configuration parameter.
type ParamType string

const (
	ParamString ParamType = "string"
	ParamInt    ParamType = "int"
	ParamBool   ParamType = "bool"
	ParamEnum   ParamType = "enum"
	// ParamStringList accepts a list of strings, and also a bare string as a
	// one-element list — writing the single value without brackets is the
	// obvious mistake and rejecting it would be needlessly hostile.
	ParamStringList ParamType = "string_list"
	// ParamDuration accepts a Go duration string such as "10s".
	ParamDuration ParamType = "duration"
	// ParamFloat accepts any number.
	ParamFloat ParamType = "float"
)

// ParamSpec declares one configuration parameter of a channel.
//
// This single declaration is the source of truth for three consumers:
// configuration validation, the generated operator form (M5), and the
// /api/v1/channels documentation endpoint (M2). Do not duplicate it.
type ParamSpec struct {
	Name     string    `json:"name"`
	Type     ParamType `json:"type"`
	Required bool      `json:"required"`
	// Private marks secrets. Values of private params are masked in logs and
	// omitted from /api/v1/channels output. Never log a private value.
	//
	// This is not decoration: router.SecretValues reads the private params out
	// of this schema and redacts their configured values from everything the
	// router emits. Declaring a parameter private is what protects it.
	Private bool   `json:"private"`
	Default any    `json:"default,omitempty"`
	Values  []string `json:"values,omitempty"` // allowed values when Type is ParamEnum
	Label   string `json:"label,omitempty"`    // human label for the operator form
	Desc    string `json:"desc,omitempty"`

	// ShowIf declares that this parameter only applies when another parameter
	// has one of a small set of values — and that, when it applies, it is
	// required unless it declares a Default.
	//
	// It exists because Required alone cannot express the shapes channels
	// actually have. A webhook's `token` is required when auth_type is bearer,
	// irrelevant otherwise, and `Required: false` says both things at once — so
	// a generated form marks nothing as required and an operator discovers the
	// mistake only when the service refuses to start.
	//
	// The Default carve-out is what separates "required" from "merely applies".
	// A webhook's `signature_header` is in play under hmac and has a sensible
	// value already, so an empty box is a fine answer; `signature_prefix` has
	// the empty string as its default for the same reason. A boolean never
	// needs one — an unchecked box is a value.
	//
	// Equality only, deliberately. A condition language would need an
	// evaluator, two implementations of it (server and form) and a story for
	// what happens when they disagree; every case in this codebase is "this
	// field applies when that enum has this value".
	ShowIf *Condition `json:"show_if,omitempty"`

	// Min and Max bound a numeric parameter. Inclusive, and nil means
	// unbounded. They apply to ParamInt and ParamFloat.
	//
	// Declared here rather than checked in the channel's own parser so that
	// the bound has one home: the parser reads it from the schema, validation
	// enforces it from the schema, and the operator form shows it — instead of
	// the parser enforcing a number no form knows about.
	//
	// ParamDuration is not bounded here; see ParamDuration for why.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// Condition is a ShowIf test: the parameter applies when the value configured
// for Field equals Equals.
//
// Equals is an `any` because the field it compares against may be an enum (the
// common case), a string or a boolean.
type Condition struct {
	Field  string `json:"field"`
	Equals any    `json:"equals"`
}

// OverflowMode declares what the core should do when a message exceeds a
// channel's length limits.
type OverflowMode int

const (
	// OverflowError rejects the message instead of mutating it.
	OverflowError OverflowMode = iota
	// OverflowTruncate cuts the body to fit.
	OverflowTruncate
	// OverflowSplit sends the body as several messages.
	OverflowSplit
)

func (m OverflowMode) String() string {
	switch m {
	case OverflowError:
		return "error"
	case OverflowTruncate:
		return "truncate"
	case OverflowSplit:
		return "split"
	default:
		return "unknown"
	}
}

// Capability is what a channel declares about itself.
//
// The core reads these values to drive format downgrade, overflow handling and
// rate limiting, so that no channel implementation ever contains that logic.
// Zero values mean "no limit" / "no constraint".
type Capability struct {
	// BodyMaxLen is a character limit; 0 means unlimited.
	BodyMaxLen int
	// BodyMaxBytes is a byte limit; 0 means unlimited.
	//
	// Both exist because the IM platforms limit different things: DingTalk
	// caps a robot message at 20000 bytes, WeCom at 4096 bytes. A character
	// count cannot express those — three bytes of a Chinese character and one
	// byte of an ASCII letter are the same "one character" to a rune counter.
	BodyMaxBytes      int
	TitleMaxLen       int              // 0 = unlimited
	SupportedFormats  []message.Format // formats the channel renders natively
	SupportAttachment bool
	RatePerSec        float64 // 0 = unlimited
	OverflowMode      OverflowMode

	// MaxRecipients is the largest number of caller-supplied recipients one
	// delivery may name. The core enforces it before the channel is called.
	//
	// Zero means something different here than it does for the limits above.
	// Everywhere else zero is "no limit"; here it means the type takes no
	// addressing from the caller at all — which is also how the core decides
	// whether a request's `to` field means anything for a given target. A
	// channel that does address recipients must therefore declare a positive
	// cap; "unlimited" is not on offer, because a caller who supplies the list
	// controls how many connections the channel opens.
	MaxRecipients int

	// NeedsRecipients reports that this instance has no destination of its own
	// and can only deliver for a caller that names one.
	//
	// An email instance with no `to` is configured for transactional mail
	// alone, and a delivery that reaches it without a recipient can only ever
	// fail. The operator surface reads this so that its test button can say so
	// rather than report a failure the operator did not cause.
	//
	// Named for the exceptional case so that the zero value is the ordinary
	// one: a channel with a fixed endpoint — which is every channel but one —
	// has nothing to set. This is an instance-level value, like the limits
	// above, so two instances of the same type can disagree.
	NeedsRecipients bool

	// MarkdownDialect is the markdown flavour the channel renders. The core
	// converts CommonMark into it, so no channel contains a converter.
	MarkdownDialect render.Dialect

	// PayloadOverhead is what the channel adds around the body when it builds
	// the payload: separators, markup, a part marker.
	//
	// The limits above describe the whole payload the platform receives, not
	// just the body, so the core subtracts the overhead (and the title, and
	// the rendered links) from the budget before it splits. Without this the
	// guarantee leaks: a body at exactly the declared limit plus a title is
	// over the limit, and the platform rejects the message the core believed
	// it had sized correctly.
	PayloadOverheadRunes int
	PayloadOverheadBytes int
}

// Supports reports whether the channel renders the given format natively.
func (c Capability) Supports(f message.Format) bool {
	for _, s := range c.SupportedFormats {
		if s == f {
			return true
		}
	}
	return false
}

// Descriptor is everything the registry knows about a channel type.
//
// ParamSchema and Capability are declared here, at registration time, so that
// documentation and validation work for a channel type that has no configured
// instance — constructing an instance to read its schema would require a valid
// configuration, which is exactly what a user reading the schema is trying to
// write.
//
// A constructed instance may report a narrower Capability than the descriptor
// (the webhook channel, for example, lets an operator set its length limits);
// the instance's value is authoritative for that instance.
type Descriptor struct {
	Type        string
	ParamSchema []ParamSpec
	Capability  Capability
	Factory     Factory

	// TargetScheme is the URL scheme that addresses this channel type in a
	// target string — "mailto" for email. Empty means the type has no URL
	// form, and a target naming this scheme is refused.
	TargetScheme string

	// ParseTarget reads a target URL into the addressing it names and the
	// instance whose transport it borrows.
	//
	// It lives on the channel because only the channel knows what its own URLs
	// mean, and that is also what lets the router dispatch on a scheme without
	// naming any channel: it looks the scheme up in the registry and calls
	// this. The returned Target carries Ref and Recipients, plus Instance when
	// the URL named one.
	ParseTarget func(raw string) (Target, error)
}

// Target is one place a message is delivered to.
//
// It is the envelope, kept separate from the message for the same reason SMTP
// keeps RCPT apart from DATA: what a notification says does not change with who
// receives it. The separation earns its keep here — the queue spools one copy
// of the message per target, so an address folded into the message would be
// delivered to every other target of the same request as well.
type Target struct {
	// Ref is the target as the caller wrote it: an instance alias, a
	// type-qualified alias, or a channel URL. Outcomes echo it back, so a
	// caller always sees the string it sent.
	Ref string

	// Instance is the configured instance to deliver through, as resolved by
	// the router. Empty means "resolve it from Ref".
	//
	// Anything that stores a target and delivers it later must fill this in at
	// the moment it accepts the delivery. Re-resolving later asks a different
	// question: "the only channel of this type" is a fact about the
	// configuration at accept time, and a second channel added in between turns
	// a queued delivery into an ambiguous one that was already acknowledged.
	Instance string

	// Recipients is addressing supplied by the caller. Only a channel that
	// declares MaxRecipients reads it; empty means the instance's own
	// configuration decides where the message goes.
	Recipients []string
}

// Channel is the only interface the core router knows about.
//
// Implementations must contain no rate limiting, no retry, no format
// conversion and no auditing — the core does all of that around them.
type Channel interface {
	// Type is the stable identifier used in configuration ("email", "dingtalk").
	// It must match the name passed to Register.
	Type() string

	// Capability declares limits and supported formats.
	Capability() Capability

	// ParamSchema declares the configuration parameters this channel accepts.
	ParamSchema() []ParamSpec

	// Send delivers the message to one target. It must classify every failure
	// into exactly one ResultClass; returning an unclassified error is a bug.
	//
	// The target is passed rather than folded into the message because it is
	// the envelope, not the content: a channel that does its own addressing
	// reads target.Recipients, and one that does not ignores the argument
	// entirely.
	Send(ctx context.Context, msg *message.Message, target Target) Result

	// Test performs a connectivity self-check without sending a real
	// notification. Used by the operator UI (M5) and by startup validation.
	Test(ctx context.Context) Result
}
