package admin

import (
	"strings"
	"testing"

	"notifyrelay/internal/admin/i18n"
	"notifyrelay/internal/channel"
)

// Every parameter this build declares has copy in every language it ships.
//
// The parameter table is a map rather than a struct, which is a deliberate
// departure from how the interface's own strings are stored. A struct field per
// parameter would be compile-checked — and it would also mean adding a channel
// type requires editing the i18n package, which is the one boundary this project
// draws hardest: a new channel is a new package and one import line, and nothing
// else changes.
//
// So the guarantee moves from the compiler to here. A missing translation is a
// test failure rather than a build failure, and in exchange a channel type
// written by somebody else still builds — its form renders in English until
// somebody adds the copy, which is the right way round for a project whose
// channel layer is meant to be extensible.
func TestParamCopy_EveryDeclaredParameterIsTranslated(t *testing.T) {
	for _, l := range i18n.Langs {
		if l == i18n.EN {
			// English is the schema itself; there is nothing to check. See
			// TestParamCopy_EnglishFallsBackToTheSchema.
			continue
		}

		var checked int
		for _, d := range channel.Descriptors() {
			for _, spec := range d.ParamSchema {
				key := i18n.ParamKey(d.Type, spec.Name)

				c, ok := i18n.LookupParam(l, d.Type, spec.Name)
				if !ok {
					t.Errorf("%s: %s has no copy — the form shows English for it", l, key)
					continue
				}
				checked++

				if strings.TrimSpace(c.Label) == "" {
					t.Errorf("%s: %s has an empty label, so the input has no name", l, key)
				}
				// A description is where the warnings live — "never inline",
				// "do not disable verification", "20 per minute". Losing one
				// loses the sentence that stops somebody making the mistake.
				if strings.TrimSpace(spec.Desc) != "" && strings.TrimSpace(c.Desc) == "" {
					t.Errorf("%s: %s has a description in the schema and none here", l, key)
				}
			}
		}

		if checked == 0 {
			t.Fatalf("%s: no parameters were checked; this test asserts nothing", l)
		}
	}
}

// A translation for a parameter that does not exist is dead weight: nothing
// renders it, and it is the entry that goes stale without anybody noticing.
func TestParamCopy_HasNothingForParametersThatDoNotExist(t *testing.T) {
	declared := map[string]bool{}
	for _, d := range channel.Descriptors() {
		for _, spec := range d.ParamSchema {
			declared[i18n.ParamKey(d.Type, spec.Name)] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("no channel types are registered; this test asserts nothing")
	}

	for _, l := range i18n.Langs {
		for key := range i18n.Params(l) {
			if !declared[key] {
				t.Errorf("%s: %q is not a parameter any registered channel declares", l, key)
			}
		}
	}
}

// English has no parameter table, and the lookup has to say so.
//
// A copy that returned an empty ParamCopy for English would blank every label
// on an English page — which is the failure the map is arranged to avoid, and
// it would be invisible until somebody switched languages.
func TestParamCopy_EnglishFallsBackToTheSchema(t *testing.T) {
	if got := i18n.Params(i18n.EN); got != nil {
		t.Errorf("Params(EN) = %v, want nil — the schema is the English", got)
	}
	if _, ok := i18n.LookupParam(i18n.EN, "webhook", "url"); ok {
		t.Error("LookupParam found English copy; there is none to find")
	}
	if _, ok := i18n.LookupParam(i18n.EN, "no-such-type", "no-such-param"); ok {
		t.Error("LookupParam invented copy for a channel type that does not exist")
	}
}

// The generated form carries the translation rather than the schema's English.
//
// This is the wiring check: the table can be complete and correct and still not
// reach the form if buildField is not given the language. It used to read the
// rendered page; the form is JSON now, so it reads the JSON — which is one
// layer closer to the thing that would break.
func TestChannelForm_ParameterLabelsComeFromTheCopyTable(t *testing.T) {
	h := newHarness(t, true)
	cookie := h.signIn(t)

	zh, _ := formIn(t, h, cookie, i18n.ZH, "")
	en, _ := formIn(t, h, cookie, i18n.EN, "")

	zhFields := map[string]fieldView{}
	for _, f := range zh.Forms {
		for _, field := range f.Fields {
			zhFields[f.Type+"."+field.Name] = field
		}
	}
	enFields := map[string]fieldView{}
	for _, f := range en.Forms {
		for _, field := range f.Fields {
			enFields[f.Type+"."+field.Name] = field
		}
	}

	var checked int
	for _, d := range channel.Descriptors() {
		for _, spec := range d.ParamSchema {
			translated, ok := i18n.LookupParam(i18n.ZH, d.Type, spec.Name)
			if !ok {
				continue
			}
			key := d.Type + "." + spec.Name

			// The Chinese form shows the translation...
			if got := zhFields[key].Label; got != translated.Label {
				t.Errorf("%s: the Chinese form shows %q, want %q", key, got, translated.Label)
			}

			// ...and the English one shows the schema's own label, unchanged.
			// A translated label leaking onto the English form would mean the
			// fallback is the wrong way round.
			want := spec.Label
			if want == "" {
				want = spec.Name
			}
			if got := enFields[key].Label; got != want {
				t.Errorf("%s: the English form shows %q, want %q", key, got, want)
			}
			checked++
		}
	}

	if checked == 0 {
		t.Fatal("no parameter was checked; this test asserts nothing")
	}
}
