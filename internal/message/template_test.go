package message

import "testing"

func TestRender(t *testing.T) {
	vars := map[string]string{"title": "deploy", "type": "warning", "payload.instance": "db-03"}

	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{name: "empty template", tmpl: "", want: ""},
		{name: "no placeholders", tmpl: "plain text", want: "plain text"},
		{name: "one placeholder", tmpl: "{title}", want: "deploy"},
		{name: "embedded", tmpl: "[{type}] {title}", want: "[warning] deploy"},
		{name: "dotted payload path", tmpl: "{payload.instance} down", want: "db-03 down"},
		{name: "repeated", tmpl: "{title}-{title}", want: "deploy-deploy"},
		{name: "adjacent", tmpl: "{title}{type}", want: "deploywarning"},

		// An unknown placeholder stays visible: a typo should be noticeable in
		// the delivered message rather than silently deleting content.
		{name: "unknown stays visible", tmpl: "{nope}", want: "{nope}"},
		{name: "unknown alongside known", tmpl: "{title} {nope}", want: "deploy {nope}"},

		// Unterminated braces are literal text.
		{name: "unterminated", tmpl: "{title", want: "{title"},
		{name: "stray close", tmpl: "title}", want: "title}"},
		{name: "empty braces", tmpl: "{}", want: "{}"},

		// A JSON template's own braces must not be mistaken for placeholders,
		// or the enclosing object would swallow everything inside it.
		{
			name: "json object",
			tmpl: `{"text": "{title}"}`,
			want: `{"text": "deploy"}`,
		},
		{
			name: "json with several fields",
			tmpl: `{"a": "{title}", "b": "{type}"}`,
			want: `{"a": "deploy", "b": "warning"}`,
		},
		{
			name: "nested json",
			tmpl: `{"outer": {"inner": "{title}"}}`,
			want: `{"outer": {"inner": "deploy"}}`,
		},
		{
			name: "braces that are not placeholders",
			tmpl: `a { b } c {title} d { e }`,
			want: `a { b } c deploy d { e }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render(tt.tmpl, vars); got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

func TestVars(t *testing.T) {
	msg := &Message{
		Title:    "deploy",
		Body:     "body text",
		Type:     TypeFailure,
		Priority: 5,
		Tags:     []string{"db", "prod"},
		Meta:     map[string]any{"instance": "db-03", "attempt": 2},
	}

	vars := msg.Vars()

	want := map[string]string{
		"title":            "deploy",
		"body":             "body text",
		"type":             "failure",
		"priority":         "5",
		"tags":             "db, prod",
		"payload.instance": "db-03",
		"payload.attempt":  "2",
	}
	for k, v := range want {
		if got := vars[k]; got != v {
			t.Errorf("vars[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestNormalizeAndValidate(t *testing.T) {
	t.Run("normalize fills defaults", func(t *testing.T) {
		msg := &Message{Title: "t", Body: "b"}
		msg.Normalize()

		if msg.Format != FormatText {
			t.Errorf("format = %q, want text", msg.Format)
		}
		if msg.Type != TypeInfo {
			t.Errorf("type = %q, want info", msg.Type)
		}
		if msg.Priority != PriorityDefault {
			t.Errorf("priority = %d, want %d", msg.Priority, PriorityDefault)
		}
	})

	t.Run("validate rejects incomplete messages", func(t *testing.T) {
		base := func() *Message {
			m := &Message{Title: "t", Body: "b"}
			m.Normalize()
			return m
		}

		if err := base().Validate(); err != nil {
			t.Fatalf("a normalised message must validate: %v", err)
		}

		noTitle := base()
		noTitle.Title = ""
		if err := noTitle.Validate(); err == nil {
			t.Error("expected an error for a missing title")
		}

		noBody := base()
		noBody.Body = "   "
		if err := noBody.Validate(); err == nil {
			t.Error("expected an error for a blank body")
		}

		badPriority := base()
		badPriority.Priority = 9
		if err := badPriority.Validate(); err == nil {
			t.Error("expected an error for an out-of-range priority")
		}
	})
}
