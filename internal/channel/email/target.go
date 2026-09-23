package email

import (
	"fmt"
	netmail "net/mail"
	"net/url"
	"strings"

	"notifyrelay/internal/channel"
)

// parseTarget reads a mailto target URL into the addressing it names.
//
//	mailto:ops@example.com
//	mailto://ops@example.com              the spelling the docs use
//	mailto:ops@example.com?via=oncall     borrow that instance's transport
//	mailto:a@x.com,b@y.com?via=oncall     several recipients, one target
//
// The two spellings mean the same thing and both are accepted, because RFC 6068
// defines `mailto:` with no authority while every example a human writes has
// the `//`.
//
// That difference is not cosmetic here. net/url implements the RFC literally:
// it parses "mailto://ops@example.com" as userinfo "ops" on host
// "example.com". So a parser reading u.Host gets the domain and no recipient,
// and a parser reading u.Opaque gets nothing at all on the form everybody
// actually writes — and a test suite written from the RFC would pass while
// every real request failed. Neither field is consulted below: the prefix is
// stripped by hand and the remainder goes to net/mail, the only parser in this
// file that understands addresses.
func parseTarget(raw string) (channel.Target, error) {
	target := channel.Target{Ref: raw}

	rest, err := stripScheme(raw)
	if err != nil {
		return target, err
	}

	// Optional authority marker, and the whole of the RFC 6068 accommodation.
	rest = strings.TrimPrefix(rest, "//")

	addrs, query, _ := strings.Cut(rest, "?")
	if err := parseQuery(query, &target); err != nil {
		return target, err
	}

	unescaped, err := url.PathUnescape(addrs)
	if err != nil {
		return target, fmt.Errorf("target %q: the address is not valid URL-escaped text: %w", raw, err)
	}
	if strings.TrimSpace(unescaped) == "" {
		return target, fmt.Errorf("target %q names no recipient", raw)
	}

	// ParseAddressList rather than a split on commas: a display name may
	// contain a comma inside quotes, and splitting first would cut it in two.
	list, err := netmail.ParseAddressList(unescaped)
	if err != nil {
		return target, fmt.Errorf("target %q: %w", raw, err)
	}
	// An address group with no members parses without complaint and yields
	// nothing — "mailto://undisclosed-recipients:;". Left alone it would look
	// exactly like a target that named nobody, and a target that names nobody
	// falls back to the instance's configured recipients: a URL that says "to
	// no one" would quietly mail the on-call mailbox.
	if len(list) == 0 {
		return target, fmt.Errorf("target %q names no recipient", raw)
	}

	for _, addr := range list {
		target.Recipients = append(target.Recipients, addr.Address)
	}

	return target, nil
}

// stripScheme removes the mailto: prefix and reports whether it was there.
func stripScheme(raw string) (string, error) {
	scheme, rest, ok := strings.Cut(raw, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(scheme), "mailto") {
		return "", fmt.Errorf("target %q is not a mailto: target", raw)
	}
	return rest, nil
}

// parseQuery reads the one parameter a mailto target is allowed to carry.
//
// url.ParseQuery rather than url.Values from u.Query(): Query() discards a
// malformed escape silently, so "?via=%zz" would become "no via" and quietly
// pick a different instance instead of reporting a broken URL.
func parseQuery(query string, target *channel.Target) error {
	if query == "" {
		return nil
	}

	values, err := url.ParseQuery(query)
	if err != nil {
		return fmt.Errorf("target %q has a malformed query: %w", target.Ref, err)
	}

	for key, vs := range values {
		switch key {
		case "via":
			if len(vs) != 1 || vs[0] == "" {
				return fmt.Errorf("target %q: \"via\" must name exactly one channel instance", target.Ref)
			}
			target.Instance = vs[0]
		case "to":
			// RFC 6068's other way of writing the recipient. Supporting both
			// would make "which one wins" a question, so only the path form is
			// accepted and this says so rather than ignoring it.
			return fmt.Errorf("target %q: put the recipient in the path "+
				"(mailto:user@example.com), not in a \"to\" parameter", target.Ref)
		default:
			// Anything else — subject, body, cc — is message content, and
			// content comes from the request body. Accepting it here would
			// silently drop it.
			return fmt.Errorf("target %q: unsupported parameter %q; only \"via\" is accepted", target.Ref, key)
		}
	}

	return nil
}
