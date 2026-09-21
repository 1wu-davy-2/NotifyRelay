package channel

import (
	"fmt"
	"time"
)

// Readers for channel configuration blocks.
//
// A channel's config arrives as map[string]any straight from YAML, so every
// value needs a type assertion. These helpers centralise that and produce
// errors that name the parameter and the type that was actually found —
// a channel that fails to start should say exactly which field is wrong.
//
// M2 layers schema-driven validation on top of these; they stay the primitive.

func typeErr(name string, got any, want string) error {
	return fmt.Errorf("parameter %q: expected %s, got %T", name, want, got)
}

// StringParam reads a required string parameter.
func StringParam(cfg map[string]any, name string) (string, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return "", fmt.Errorf("parameter %q is required", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", typeErr(name, v, "a string")
	}
	if s == "" {
		return "", fmt.Errorf("parameter %q must not be empty", name)
	}
	return s, nil
}

// StringParamOr reads an optional string parameter.
func StringParamOr(cfg map[string]any, name, def string) (string, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", typeErr(name, v, "a string")
	}
	return s, nil
}

// IntParamOr reads an optional integer parameter.
//
// YAML decodes whole numbers as int, but a value written as 587.0 would arrive
// as float64; that is accepted rather than rejected, since it is unambiguous.
func IntParamOr(cfg map[string]any, name string, def int) (int, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return def, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("parameter %q: expected a whole number, got %v", name, n)
		}
		return int(n), nil
	default:
		return 0, typeErr(name, v, "an integer")
	}
}

// IntParamBounded reads an optional integer parameter and applies the bounds
// its ParamSpec declares.
//
// This is how a channel's parser enforces a range without owning a copy of it.
// The bound lives in paramSchema — the same declaration the operator form is
// generated from and the same one /api/v1/channels documents — so there is one
// number rather than three that drift.
func IntParamBounded(cfg map[string]any, specs []ParamSpec, name string, def int) (int, error) {
	n, err := IntParamOr(cfg, name, def)
	if err != nil {
		return 0, err
	}
	if err := rangeIn(specs, name, float64(n)); err != nil {
		return 0, err
	}
	return n, nil
}

// FloatParamBounded reads an optional numeric parameter and applies the bounds
// its ParamSpec declares.
func FloatParamBounded(cfg map[string]any, specs []ParamSpec, name string, def float64) (float64, error) {
	n, err := FloatParamOr(cfg, name, def)
	if err != nil {
		return 0, err
	}
	if err := rangeIn(specs, name, n); err != nil {
		return 0, err
	}
	return n, nil
}

// rangeIn applies the named parameter's declared bounds. A name with no spec,
// or a spec with no bounds, is unconstrained.
func rangeIn(specs []ParamSpec, name string, n float64) error {
	for _, s := range specs {
		if s.Name == name {
			return checkRange(s, n)
		}
	}
	return nil
}

// FloatParamOr reads an optional numeric parameter.
func FloatParamOr(cfg map[string]any, name string, def float64) (float64, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return def, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	default:
		return 0, typeErr(name, v, "a number")
	}
}

// BoolParamOr reads an optional boolean parameter.
func BoolParamOr(cfg map[string]any, name string, def bool) (bool, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, typeErr(name, v, "a boolean")
	}
	return b, nil
}

// DurationParamOr reads an optional duration parameter written as a string
// such as "10s".
//
// A non-positive duration is refused here rather than by each caller. Every
// duration in this service is a timeout, and "wait no time at all" is not a
// shorter timeout — it is a missing one, which reads as an immediate failure
// rather than as a mistake. Six channels were checking this individually.
func DurationParamOr(cfg map[string]any, name string, def time.Duration) (time.Duration, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, typeErr(name, v, "a duration string such as \"10s\"")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %w", name, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("parameter %q must be greater than zero, got %q", name, s)
	}
	return d, nil
}

// StringSliceParam reads an optional list of strings.
//
// A single string is accepted as a one-element list, because writing
// `to: ops@example.com` instead of `to: [ops@example.com]` is the obvious
// mistake and rejecting it would be needlessly hostile.
func StringSliceParam(cfg map[string]any, name string) ([]string, error) {
	v, ok := cfg[name]
	if !ok || v == nil {
		return nil, nil
	}

	switch t := v.(type) {
	case string:
		if t == "" {
			return nil, nil
		}
		return []string{t}, nil

	case []any:
		out := make([]string, 0, len(t))
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("parameter %q[%d]: expected a string, got %T", name, i, item)
			}
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil

	case []string:
		return t, nil

	default:
		return nil, typeErr(name, v, "a string or a list of strings")
	}
}
