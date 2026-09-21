package channel

import (
	"strings"
	"testing"
)

func TestValidateParams_AcceptsAWellFormedBlock(t *testing.T) {
	specs := []ParamSpec{
		{Name: "host", Type: ParamString, Required: true},
		{Name: "port", Type: ParamInt},
		{Name: "enabled", Type: ParamBool},
		{Name: "mode", Type: ParamEnum, Values: []string{"a", "b"}},
		{Name: "timeout", Type: ParamDuration},
		{Name: "ratio", Type: ParamFloat},
		{Name: "to", Type: ParamStringList},
	}

	err := ValidateParams("test", specs, map[string]any{
		"host": "example.com", "port": 587, "enabled": true, "mode": "a",
		"timeout": "10s", "ratio": 1.5, "to": []any{"x@y.z"},
	})
	if err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
}

// The check that earns its keep: a channel constructor reading the fields it
// knows about will happily ignore a typo'd key, and the service then runs with
// the wrong configuration and no indication why.
func TestValidateParams_RejectsUnknownKeys(t *testing.T) {
	specs := []ParamSpec{{Name: "host", Type: ParamString, Required: true}}

	err := ValidateParams("test", specs, map[string]any{
		"host": "example.com",
		"hots": "typo.example.com",
	})
	if err == nil {
		t.Fatal("an unknown parameter must be rejected")
	}
	if !strings.Contains(err.Error(), "hots") {
		t.Errorf("error should name the offending key, got: %v", err)
	}
	if !strings.Contains(err.Error(), "host") {
		t.Errorf("error should list the known parameters, got: %v", err)
	}
}

func TestValidateParams_ReportsEveryProblemAtOnce(t *testing.T) {
	specs := []ParamSpec{
		{Name: "host", Type: ParamString, Required: true},
		{Name: "mode", Type: ParamEnum, Values: []string{"a", "b"}},
		{Name: "timeout", Type: ParamDuration},
		{Name: "port", Type: ParamInt},
	}

	err := ValidateParams("test", specs, map[string]any{
		"mode":    "z",
		"timeout": "soon",
		"port":    "eighty",
		"nope":    true,
	})
	if err == nil {
		t.Fatal("expected errors")
	}

	// One restart per typo would be a miserable way to configure a service.
	for _, want := range []string{"host", "mode", "timeout", "port", "nope"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%v", want, err)
		}
	}
}

func TestValidateParams_TypeChecks(t *testing.T) {
	specs := []ParamSpec{
		{Name: "s", Type: ParamString},
		{Name: "i", Type: ParamInt},
		{Name: "b", Type: ParamBool},
		{Name: "e", Type: ParamEnum, Values: []string{"a"}},
		{Name: "d", Type: ParamDuration},
		{Name: "f", Type: ParamFloat},
		{Name: "l", Type: ParamStringList},
	}

	tests := []struct {
		name    string
		value   map[string]any
		wantErr bool
	}{
		{name: "string ok", value: map[string]any{"s": "x"}},
		{name: "string bad", value: map[string]any{"s": 1}, wantErr: true},
		{name: "int ok", value: map[string]any{"i": 1}},
		{name: "int as float ok", value: map[string]any{"i": float64(2)}},
		{name: "int fractional bad", value: map[string]any{"i": 1.5}, wantErr: true},
		{name: "int as string bad", value: map[string]any{"i": "1"}, wantErr: true},
		{name: "bool ok", value: map[string]any{"b": true}},
		{name: "bool bad", value: map[string]any{"b": "true"}, wantErr: true},
		{name: "enum ok", value: map[string]any{"e": "a"}},
		{name: "enum bad", value: map[string]any{"e": "z"}, wantErr: true},
		{name: "duration ok", value: map[string]any{"d": "5s"}},
		{name: "duration bad", value: map[string]any{"d": "5"}, wantErr: true},
		{name: "float ok", value: map[string]any{"f": 0.5}},
		{name: "float as int ok", value: map[string]any{"f": 2}},
		{name: "float as string bad", value: map[string]any{"f": "0.5"}, wantErr: true},
		{name: "list ok", value: map[string]any{"l": []any{"a", "b"}}},
		{name: "bare string as list ok", value: map[string]any{"l": "a"}},
		{name: "list of non-strings bad", value: map[string]any{"l": []any{1}}, wantErr: true},
		{name: "list as number bad", value: map[string]any{"l": 3}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateParams("test", specs, tt.value)
			if tt.wantErr && err == nil {
				t.Errorf("expected an error for %+v", tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %+v: %v", tt.value, err)
			}
		})
	}
}

func TestValidateParams_OptionalMayBeAbsent(t *testing.T) {
	specs := []ParamSpec{
		{Name: "required", Type: ParamString, Required: true},
		{Name: "optional", Type: ParamString},
	}

	if err := ValidateParams("test", specs, map[string]any{"required": "x"}); err != nil {
		t.Errorf("an absent optional parameter must be fine: %v", err)
	}

	err := ValidateParams("test", specs, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("an absent required parameter must be reported, got: %v", err)
	}

	// A key present but null is the same as absent.
	if err := ValidateParams("test", specs, map[string]any{"required": "x", "optional": nil}); err != nil {
		t.Errorf("a null optional parameter must be fine: %v", err)
	}
}
