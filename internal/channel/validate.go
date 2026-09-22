package channel

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ValidateParams checks a configuration block against a channel's schema.
//
// The schema is doing its job here: the same declaration that documents a
// channel also decides whether a configuration is legal. The check that earns
// its keep is the unknown-key one — a channel constructor reading the fields
// it knows about will happily ignore a `hots:` typo, and the service then runs
// for a week with the wrong host and no indication why.
//
// Every problem is reported at once, so an operator can fix the file in one
// pass instead of one restart per typo.
func ValidateParams(channelType string, specs []ParamSpec, cfg map[string]any) error {
	known := make(map[string]bool, len(specs))
	for _, s := range specs {
		known[s.Name] = true
	}

	var errs []error

	if unknown := unknownKeys(cfg, known); len(unknown) > 0 {
		errs = append(errs, fmt.Errorf("unknown parameter(s) %s (known: %s)",
			quoteJoin(unknown), quoteJoin(specNames(specs))))
	}

	for _, spec := range specs {
		value, present := cfg[spec.Name]
		if !present || value == nil {
			if spec.Required {
				errs = append(errs, fieldErr(spec.Name, "parameter %q is required", spec.Name))
			}
			continue
		}
		if err := checkValue(spec, value); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func checkValue(spec ParamSpec, value any) error {
	switch spec.Type {
	case ParamString:
		if _, ok := value.(string); !ok {
			return typeMismatch(spec, value, "a string")
		}

	case ParamInt:
		var n int
		switch v := value.(type) {
		case int:
			n = v
		case int64:
			n = int(v)
		case float64:
			if v != float64(int(v)) {
				return fieldErr(spec.Name, "parameter %q: expected a whole number, got %v", spec.Name, v)
			}
			n = int(v)
		default:
			return typeMismatch(spec, value, "an integer")
		}
		if err := checkRange(spec, float64(n)); err != nil {
			return err
		}

	case ParamBool:
		if _, ok := value.(bool); !ok {
			return typeMismatch(spec, value, "a boolean")
		}

	case ParamEnum:
		s, ok := value.(string)
		if !ok {
			return typeMismatch(spec, value, "a string")
		}
		for _, allowed := range spec.Values {
			if s == allowed {
				return nil
			}
		}
		return fieldErr(spec.Name, "parameter %q: %q is not one of %s", spec.Name, s, quoteJoin(spec.Values))

	case ParamDuration:
		s, ok := value.(string)
		if !ok {
			return typeMismatch(spec, value, "a duration string such as \"10s\"")
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return wrapFieldErr(spec.Name, err, "parameter %q: %v", spec.Name, err)
		}
		if d <= 0 {
			return fieldErr(spec.Name, "parameter %q must be greater than zero, got %q", spec.Name, s)
		}

	case ParamFloat:
		var n float64
		switch v := value.(type) {
		case int:
			n = float64(v)
		case int64:
			n = float64(v)
		case float64:
			n = v
		default:
			return typeMismatch(spec, value, "a number")
		}
		if err := checkRange(spec, n); err != nil {
			return err
		}

	case ParamStringList:
		switch v := value.(type) {
		case string, []string:
			// A bare string is accepted as a one-element list, matching the
			// readers in params.go.
		case []any:
			for i, item := range v {
				if _, ok := item.(string); !ok {
					return fieldErr(spec.Name, "parameter %q[%d]: expected a string, got %T", spec.Name, i, item)
				}
			}
		default:
			return typeMismatch(spec, value, "a string or a list of strings")
		}

	default:
		return fieldErr(spec.Name, "parameter %q: unknown declared type %q", spec.Name, spec.Type)
	}

	return nil
}

func typeMismatch(spec ParamSpec, value any, want string) error {
	return fieldErr(spec.Name, "parameter %q: expected %s, got %T", spec.Name, want, value)
}

// checkRange applies the bounds the parameter declares.
//
// The message names the bound rather than just refusing: "must be at least 1"
// tells an operator what to write, and "invalid value" makes them read the
// source.
func checkRange(spec ParamSpec, n float64) error {
	if spec.Min != nil && n < *spec.Min {
		return fieldErr(spec.Name, "parameter %q must be at least %s, got %s",
			spec.Name, formatBound(*spec.Min), formatBound(n))
	}
	if spec.Max != nil && n > *spec.Max {
		return fieldErr(spec.Name, "parameter %q must be at most %s, got %s",
			spec.Name, formatBound(*spec.Max), formatBound(n))
	}
	return nil
}

// formatBound prints a bound without a trailing ".0" on whole numbers, so a
// port says "65535" rather than "65535.0".
func formatBound(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func unknownKeys(cfg map[string]any, known map[string]bool) []string {
	var out []string
	for name := range cfg {
		if !known[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func specNames(specs []ParamSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

func quoteJoin(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return strings.Join(quoted, ", ")
}
