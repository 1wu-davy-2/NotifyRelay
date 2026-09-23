package channel

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Factory builds a channel instance from its configuration block.
//
// instance is the operator-chosen alias ("oncall", "dev-group"); it is used
// only for error messages and audit records.
type Factory func(instance string, cfg map[string]any) (Channel, error)

var (
	mu       sync.RWMutex
	registry = make(map[string]Descriptor)
	// schemes indexes descriptors by their target URL scheme.
	//
	// Built here rather than searched on demand because it is read on the path
	// of every delivery that arrives as a URL, and because a duplicate scheme
	// is a registration error worth catching at startup: two channel types
	// claiming "mailto" would make a target's meaning depend on init order.
	schemes = make(map[string]Descriptor)
)

// Register makes a channel type available under the given type name.
//
// Call it from the channel package's init(). Registering the same type twice
// panics rather than overwriting: a silent override makes it ambiguous which
// implementation is live, and that failure is far worse than refusing to start.
func Register(d Descriptor) {
	if d.Type == "" {
		panic("channel: Register called with an empty type name")
	}
	if d.Factory == nil {
		panic(fmt.Sprintf("channel: Register called with a nil factory for type %q", d.Type))
	}
	if err := checkSchema(d.Type, d.ParamSchema); err != nil {
		panic(err)
	}
	if err := checkTargetScheme(d); err != nil {
		panic(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[d.Type]; dup {
		panic(fmt.Sprintf("channel: duplicate registration for type %q", d.Type))
	}
	if d.TargetScheme != "" {
		if _, dup := schemes[d.TargetScheme]; dup {
			panic(fmt.Sprintf("channel: type %q claims URL scheme %q, which is already taken",
				d.Type, d.TargetScheme))
		}
		schemes[d.TargetScheme] = d
	}
	registry[d.Type] = d
}

// checkTargetScheme rejects a half-declared URL form at registration time.
//
// A scheme with no parser would parse a target into nothing, and a parser with
// no scheme would never be called at all — the second is the worse of the two,
// because the channel author believes they have shipped a feature.
func checkTargetScheme(d Descriptor) error {
	if d.TargetScheme == "" && d.ParseTarget == nil {
		return nil
	}
	if d.TargetScheme == "" {
		return fmt.Errorf("channel %q: declares a target parser but no scheme", d.Type)
	}
	if d.ParseTarget == nil {
		return fmt.Errorf("channel %q: declares target scheme %q but no parser to read it",
			d.Type, d.TargetScheme)
	}
	if d.TargetScheme != strings.ToLower(d.TargetScheme) || strings.ContainsAny(d.TargetScheme, ": /") {
		return fmt.Errorf("channel %q: target scheme %q must be lowercase and free of colons and slashes",
			d.Type, d.TargetScheme)
	}
	return nil
}

// checkSchema rejects a self-contradictory schema at registration time, so a
// channel author finds out at startup rather than an operator finding out from
// a generated form that cannot be satisfied.
func checkSchema(channelType string, specs []ParamSpec) error {
	seen := make(map[string]bool, len(specs))
	for _, s := range specs {
		if s.Name == "" {
			return fmt.Errorf("channel %q: a parameter spec has an empty name", channelType)
		}
		if seen[s.Name] {
			return fmt.Errorf("channel %q: parameter %q is declared twice", channelType, s.Name)
		}
		seen[s.Name] = true

		switch s.Type {
		case ParamString, ParamInt, ParamBool, ParamStringList, ParamDuration, ParamFloat:
		case ParamEnum:
			if len(s.Values) == 0 {
				return fmt.Errorf("channel %q: enum parameter %q declares no values", channelType, s.Name)
			}
		default:
			return fmt.Errorf("channel %q: parameter %q has unknown type %q", channelType, s.Name, s.Type)
		}

		if s.Min != nil && s.Max != nil && *s.Min > *s.Max {
			return fmt.Errorf("channel %q: parameter %q declares min %v above max %v",
				channelType, s.Name, *s.Min, *s.Max)
		}
		if (s.Min != nil || s.Max != nil) && s.Type != ParamInt && s.Type != ParamFloat {
			return fmt.Errorf("channel %q: parameter %q declares min/max but has type %q",
				channelType, s.Name, s.Type)
		}
	}

	// A second pass, because a condition may name a parameter declared later.
	for _, s := range specs {
		if s.ShowIf == nil {
			continue
		}
		if s.ShowIf.Field == "" {
			return fmt.Errorf("channel %q: parameter %q has a ShowIf with no field",
				channelType, s.Name)
		}
		if s.ShowIf.Equals == nil {
			return fmt.Errorf("channel %q: parameter %q has a ShowIf with no value to match",
				channelType, s.Name)
		}
		if s.ShowIf.Field == s.Name {
			return fmt.Errorf("channel %q: parameter %q has a ShowIf on itself",
				channelType, s.Name)
		}
		if !seen[s.ShowIf.Field] {
			// A condition on a name that does not exist hides the field for
			// every configuration, silently. Nothing downstream would notice.
			return fmt.Errorf("channel %q: parameter %q has a ShowIf on unknown parameter %q",
				channelType, s.Name, s.ShowIf.Field)
		}
	}

	return nil
}

// New builds an instance of a registered channel type.
func New(channelType, instance string, cfg map[string]any) (Channel, error) {
	mu.RLock()
	d, ok := registry[channelType]
	mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("channel: unknown type %q (registered types: %v)", channelType, Registered())
	}

	ch, err := d.Factory(instance, cfg)
	if err != nil {
		return nil, fmt.Errorf("channel %q (type %q): %w", instance, channelType, err)
	}
	if ch == nil {
		return nil, fmt.Errorf("channel %q (type %q): factory returned a nil channel", instance, channelType)
	}
	if got := ch.Type(); got != channelType {
		return nil, fmt.Errorf("channel %q: registered as %q but reports type %q", instance, channelType, got)
	}
	return ch, nil
}

// Registered returns the sorted list of registered channel types.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Descriptors returns every registered channel type, sorted by type name.
func Descriptors() []Descriptor {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Descriptor, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// LookupScheme returns the descriptor that answers to a target URL scheme.
//
// This is the whole of the router's knowledge about URLs: it cuts a scheme off
// a target, asks here, and lets the channel read the rest. A router that knew
// "mailto" would be a router that has to change when a channel is added.
func LookupScheme(scheme string) (Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()

	d, ok := schemes[strings.ToLower(scheme)]
	return d, ok
}

// Lookup returns the descriptor for a channel type.
func Lookup(channelType string) (Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()

	d, ok := registry[channelType]
	return d, ok
}

// IsRegistered reports whether a channel type is available in this binary.
func IsRegistered(channelType string) bool {
	_, ok := Lookup(channelType)
	return ok
}
