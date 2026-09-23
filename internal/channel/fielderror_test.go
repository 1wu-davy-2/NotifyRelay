package channel

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func bounds(min, max float64) *ParamSpec {
	return &ParamSpec{Name: "port", Type: ParamInt, Min: &min, Max: &max}
}

// The messages did not change when the type did.
//
// FieldError exists so a form can put a complaint under the right box. It is
// not a reason to reword anything: these strings are quoted in API responses,
// written to logs and asserted by tests, and a version that reads well under an
// input would read badly in all three. This pins them.
func TestFieldErrors_KeepTheMessagesTheyAlwaysHad(t *testing.T) {
	specs := []ParamSpec{
		{Name: "host", Type: ParamString, Required: true},
		{Name: "port", Type: ParamInt, Min: bounds(1, 65535).Min, Max: bounds(1, 65535).Max},
		{Name: "mode", Type: ParamEnum, Values: []string{"a", "b"}},
	}

	for _, tc := range []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			"missing required",
			map[string]any{"port": 25, "mode": "a"},
			`parameter "host" is required`,
		},
		{
			"out of range",
			map[string]any{"host": "h", "port": 70000, "mode": "a"},
			`parameter "port" must be at most 65535, got 70000`,
		},
		{
			"not an allowed value",
			map[string]any{"host": "h", "port": 25, "mode": "c"},
			`parameter "mode": "c" is not one of "a", "b"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateParams("test", specs, tc.cfg)
			if err == nil {
				t.Fatal("no error")
			}
			if err.Error() != tc.want {
				t.Errorf("message = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

// A failure that names a parameter says which one, and a failure that does not
// is still reported.
//
// The second half is the one worth having a test for. A configuration can be
// wrong in a way that belongs to no single field — an unknown key is the
// obvious one — and dropping those because they do not fit the per-field shape
// would lose the reason the save was refused.
func TestSplitErrors_SeparatesNamedFromUnnamed(t *testing.T) {
	specs := []ParamSpec{
		{Name: "host", Type: ParamString, Required: true},
		{Name: "port", Type: ParamInt, Min: bounds(1, 65535).Min, Max: bounds(1, 65535).Max},
	}

	// One missing field, one out of range, and one key the schema never
	// declared. Three problems, reported together.
	err := ValidateParams("test", specs, map[string]any{
		"port": 70000,
		"hots": "typo",
	})
	if err == nil {
		t.Fatal("no error")
	}

	named, other := SplitErrors(err)

	byField := map[string]string{}
	for _, fe := range named {
		byField[fe.Field] = fe.Message
	}

	if len(byField) != 2 {
		t.Fatalf("named %d fields, want 2 (host, port): %v", len(byField), byField)
	}
	if !strings.Contains(byField["host"], `"host" is required`) {
		t.Errorf("host = %q", byField["host"])
	}
	if !strings.Contains(byField["port"], "at most 65535") {
		t.Errorf("port = %q", byField["port"])
	}

	if len(other) != 1 {
		t.Fatalf("unnamed = %v, want the unknown key on its own", other)
	}
	if !strings.Contains(other[0], "hots") {
		t.Errorf("unnamed = %q, want it to name the key that is not declared", other[0])
	}

	// Every problem is in exactly one of the two lists. A message that appeared
	// in neither would be a refusal with no reason attached.
	joined := strings.Join(other, "\n")
	for _, fe := range named {
		if strings.Contains(joined, fe.Message) {
			t.Errorf("%q is in both lists", fe.Message)
		}
	}
}

// A channel constructor's own failure keeps its cause.
//
// Those are written by hand in each channel package and wrapped by
// fmt.Errorf("%w"); turning one into a FieldError must not turn errors.Is into
// a string comparison.
func TestFieldError_KeepsTheCause(t *testing.T) {
	cause := errors.New("the underlying failure")
	err := wrapFieldErr("host", cause, "parameter %q: %v", "host", cause)

	if !errors.Is(err, cause) {
		t.Error("the cause is not reachable through errors.Is")
	}

	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatal("the error is not a FieldError")
	}
	if fe.Field != "host" {
		t.Errorf("Field = %q, want host", fe.Field)
	}
}

// A nil error has nothing to split, and neither does an error that is only a
// wrapper.
func TestSplitErrors_HandlesTheEmptyCases(t *testing.T) {
	if fields, other := SplitErrors(nil); len(fields) != 0 || len(other) != 0 {
		t.Errorf("SplitErrors(nil) = %v, %v", fields, other)
	}

	plain := fmt.Errorf("something else entirely")
	fields, other := SplitErrors(plain)
	if len(fields) != 0 {
		t.Errorf("fields = %v, want none", fields)
	}
	if len(other) != 1 || other[0] != plain.Error() {
		t.Errorf("other = %v, want the message", other)
	}
}
