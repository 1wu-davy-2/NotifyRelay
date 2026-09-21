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
	// has one of a small set of values, and that it is required when it does.
	//
	// It exists because Required alone cannot express the shapes channels
	// actually have. A webhook's `token` is required when auth_type is bearer,
	// irrelevant otherwise, and `Required: false` says both things at once — so
	// a generated form marks nothing as required and an operator discovers the
	// mistake only when the service refuses to start.
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

	// Send delivers the message. It must classify every failure into exactly
	// one ResultClass; returning an unclassified error is a bug.
	Send(ctx context.Context, msg *message.Message) Result

	// Test performs a connectivity self-check without sending a real
	// notification. Used by the operator UI (M5) and by startup validation.
	Test(ctx context.Context) Result
}
