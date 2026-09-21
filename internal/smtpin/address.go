// Package smtpin accepts notifications over SMTP.
//
// The routing syntax is taken from YoRyan/mailrise (MIT — see NOTICE): the
// recipient address carries the routing instruction, so any system that can
// send mail can address a channel without learning a new header convention.
package smtpin

import (
	"fmt"
	"strings"

	"notifyrelay/internal/message"
)

// ParseRecipient splits a recipient address into a channel alias and a
// message type.
//
//	oncall@relay.local           -> alias "oncall", type info
//	oncall.failure@relay.local   -> alias "oncall", type failure
//	ops.team.warning@relay.local -> alias "ops.team", type warning
//
// The local part is <alias>[.<type>]. A trailing segment that is not one of
// the four message types is treated as part of the alias, so dotted aliases
// work without escaping.
func ParseRecipient(addr string) (string, message.Type, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", "", fmt.Errorf("empty recipient address")
	}

	local := addr
	if at := strings.LastIndex(addr, "@"); at >= 0 {
		local = addr[:at]
	}
	if local == "" {
		return "", "", fmt.Errorf("recipient %q has an empty local part", addr)
	}

	alias, typ := local, message.TypeInfo
	if dot := strings.LastIndex(local, "."); dot > 0 {
		if t, ok := parseType(local[dot+1:]); ok {
			alias, typ = local[:dot], t
		}
	}

	if alias == "" {
		return "", "", fmt.Errorf("recipient %q has an empty channel alias", addr)
	}
	// A leading or trailing dot can only be a typo, and accepting it would
	// surface later as a baffling "unknown channel" message.
	if strings.HasPrefix(alias, ".") || strings.HasSuffix(alias, ".") {
		return "", "", fmt.Errorf("recipient %q has a malformed channel alias %q", addr, alias)
	}
	return alias, typ, nil
}

func parseType(s string) (message.Type, bool) {
	switch message.Type(strings.ToLower(s)) {
	case message.TypeInfo:
		return message.TypeInfo, true
	case message.TypeSuccess:
		return message.TypeSuccess, true
	case message.TypeWarning:
		return message.TypeWarning, true
	case message.TypeFailure:
		return message.TypeFailure, true
	default:
		return "", false
	}
}
