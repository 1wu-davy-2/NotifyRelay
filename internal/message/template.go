package message

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Render substitutes {placeholders} in a template.
//
// Deliberately logic-free: no conditionals, no loops, no helper functions.
// Anything more complex belongs in the caller, not in a notification template.
//
// An unknown placeholder is left visible in the output rather than replaced
// with an empty string, so a typo shows up in the delivered message instead of
// silently dropping content.
//
// The same property covers JSON templates. In `{"text": "{title}"}` the first
// brace belongs to the JSON object, not to a placeholder, and the text between
// the two braces is not a name. Only a brace pair whose contents look like a
// placeholder name is treated as one, so an enclosing object brace cannot
// swallow the placeholders inside it.
func Render(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(tmpl))

	for i := 0; i < len(tmpl); {
		open := strings.IndexByte(tmpl[i:], '{')
		if open < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		open += i

		closing := strings.IndexByte(tmpl[open:], '}')
		if closing < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		closing += open

		key := tmpl[open+1 : closing]
		if !isPlaceholderName(key) {
			// Stray punctuation between braces, not a placeholder. Emit the
			// brace and resume from the next character rather than skipping
			// to the closing one.
			b.WriteString(tmpl[i : open+1])
			i = open + 1
			continue
		}

		b.WriteString(tmpl[i:open])
		if v, ok := vars[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(tmpl[open : closing+1])
		}

		i = closing + 1
	}

	return b.String()
}

// isPlaceholderName reports whether the text between two braces is a
// placeholder name rather than punctuation that happens to sit between them.
func isPlaceholderName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// RenderJSON is Render with each substituted value escaped for use inside a
// JSON string literal.
//
// Without this, a body containing a quote or a newline would produce a payload
// that is not valid JSON — and the failure would surface at the receiving end
// as a parse error, far from the template that caused it.
//
// Values are escaped as string content, so placeholders must sit inside quotes:
//
//	{"text": "{body}"}     correct
//	{"priority": {priority}}   wrong — the escaped value is not a number
func RenderJSON(tmpl string, vars map[string]string) string {
	if tmpl == "" {
		return ""
	}

	escaped := make(map[string]string, len(vars))
	for k, v := range vars {
		encoded, err := json.Marshal(v)
		if err != nil {
			escaped[k] = v
			continue
		}
		// Strip the surrounding quotes: the template supplies its own.
		escaped[k] = string(encoded[1 : len(encoded)-1])
	}
	return Render(tmpl, escaped)
}

// Vars returns the placeholder set for a message.
//
// Message.Meta is exposed under the "payload." prefix, so a caller that puts
// {"instance": "db-03"} in Meta can write {payload.instance} in a template.
func (m *Message) Vars() map[string]string {
	vars := map[string]string{
		"title":    m.Title,
		"body":     m.Body,
		"type":     string(m.Type),
		"priority": strconv.Itoa(int(m.Priority)),
		"tags":     strings.Join(m.Tags, ", "),
	}
	for k, v := range m.Meta {
		vars["payload."+k] = fmt.Sprint(v)
	}
	return vars
}
