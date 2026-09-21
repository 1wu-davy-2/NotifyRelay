package channel

import (
	"fmt"
	"strings"
	"testing"
)

func noopFactory(string, map[string]any) (Channel, error) { return nil, nil }

func TestRegister_PanicsOnDuplicateType(t *testing.T) {
	const typeName = "registrytest-duplicate"

	Register(Descriptor{Type: typeName, Factory: noopFactory})

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("registering the same type twice must panic rather than silently overwrite")
		}
		if !strings.Contains(fmt.Sprint(recovered), typeName) {
			t.Errorf("panic should name the type, got: %v", recovered)
		}
	}()

	Register(Descriptor{Type: typeName, Factory: noopFactory})
}

func TestRegister_RejectsIncompleteDescriptors(t *testing.T) {
	tests := []struct {
		name string
		desc Descriptor
	}{
		{name: "empty type", desc: Descriptor{Factory: noopFactory}},
		{name: "nil factory", desc: Descriptor{Type: "registrytest-nilfactory"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected Register to panic")
				}
			}()
			Register(tt.desc)
		})
	}
}

// A schema that contradicts itself should fail at registration, so the channel
// author finds out at startup rather than an operator finding out from a form
// that cannot be filled in.
func TestRegister_RejectsSelfContradictorySchema(t *testing.T) {
	tests := []struct {
		name  string
		specs []ParamSpec
	}{
		{
			name:  "duplicate parameter",
			specs: []ParamSpec{{Name: "a", Type: ParamString}, {Name: "a", Type: ParamString}},
		},
		{
			name:  "empty parameter name",
			specs: []ParamSpec{{Name: "", Type: ParamString}},
		},
		{
			name:  "unknown type",
			specs: []ParamSpec{{Name: "a", Type: "uuid"}},
		},
		{
			name:  "enum with no values",
			specs: []ParamSpec{{Name: "a", Type: ParamEnum}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected Register to panic on a self-contradictory schema")
				}
			}()
			Register(Descriptor{
				Type:        "registrytest-schema-" + strings.ReplaceAll(tt.name, " ", "-"),
				ParamSchema: tt.specs,
				Factory:     noopFactory,
			})
		})
	}
}

func TestDescriptors_AreSortedAndLookupable(t *testing.T) {
	a := "registrytest-alpha"
	b := "registrytest-beta"
	Register(Descriptor{Type: b, ParamSchema: []ParamSpec{{Name: "x", Type: ParamString}}, Factory: noopFactory})
	Register(Descriptor{Type: a, Factory: noopFactory})

	got := Registered()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Registered() is not sorted: %v", got)
		}
	}

	d, ok := Lookup(a)
	if !ok || d.Type != a {
		t.Fatalf("Lookup(%q) = %+v, %v", a, d, ok)
	}
	if len(d.ParamSchema) != 0 {
		t.Errorf("descriptor for %q should carry its own schema, got %+v", a, d.ParamSchema)
	}

	if _, ok := Lookup("registrytest-does-not-exist"); ok {
		t.Error("Lookup should not find an unregistered type")
	}
}

// A descriptor must be reachable without constructing an instance — that is
// the whole reason /api/v1/channels can document a channel nobody configured.
func TestDescriptors_DocumentAChannelWithNoValidConfiguration(t *testing.T) {
	const typeName = "registrytest-needs-config"
	Register(Descriptor{
		Type:        typeName,
		ParamSchema: []ParamSpec{{Name: "required_thing", Type: ParamString, Required: true}},
		Factory: func(string, map[string]any) (Channel, error) {
			return nil, fmt.Errorf("this channel cannot be constructed")
		},
	})

	descriptors := Descriptors()
	for _, d := range descriptors {
		if d.Type != typeName {
			continue
		}
		if len(d.ParamSchema) != 1 || d.ParamSchema[0].Name != "required_thing" {
			t.Fatalf("descriptor schema = %+v", d.ParamSchema)
		}
		return
	}
	t.Fatalf("%q missing from Descriptors(); a type with no usable configuration must still be documented", typeName)
}
