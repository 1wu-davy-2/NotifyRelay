package channel

import (
	"errors"
	"fmt"
	"sort"
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
				errs = append(errs, fmt.Errorf("parameter %q is required", spec.Name))
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
		switch n := value.(type) {
		case int, int64:
		case float64:
			if n != float64(int(n)) {
				return fmt.Errorf("parameter %q: expected a whole number, got %v", spec.Name, n)
			}
		default:
			return typeMismatch(spec, value, "an integer")
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
		return fmt.Errorf("parameter %q: %q is not one of %s", spec.Name, s, quoteJoin(spec.Values))

	case ParamDuration:
		s, ok := value.(string)
		if !ok {
			return typeMismatch(spec, value, "a duration string such as \"10s\"")
		}
		if _, err := time.ParseDuration(s); err != nil {
			return fmt.Errorf("parameter %q: %w", spec.Name, err)
		}

	case ParamFloat:
		switch value.(type) {
		case int, int64, float64:
		default:
			return typeMismatch(spec, value, "a number")
		}

	case ParamStringList:
		switch v := value.(type) {
		case string, []string:
			// A bare string is accepted as a one-element list, matching the
			// readers in params.go.
		case []any:
			for i, item := range v {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("parameter %q[%d]: expected a string, got %T", spec.Name, i, item)
				}
			}
		default:
			return typeMismatch(spec, value, "a string or a list of strings")
		}

	default:
		return fmt.Errorf("parameter %q: unknown declared type %q", spec.Name, spec.Type)
	}

	return nil
}

func typeMismatch(spec ParamSpec, value any, want string) error {
	return fmt.Errorf("parameter %q: expected %s, got %T", spec.Name, want, value)
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
