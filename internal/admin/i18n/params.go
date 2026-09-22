package i18n

// ParamCopy is one channel parameter's label and description in one language.
type ParamCopy struct {
	Label string
	Desc  string
}

// ParamKey names a parameter in the copy tables: the channel type, a dot, the
// parameter's schema name.
//
// Keyed by type as well as by name because the same name does not mean the same
// thing twice. `timeout` is a request timeout for a webhook and a delivery
// timeout for SMTP; `token` is a Slack bot token in one channel and a bearer
// token in another. Keying by name alone would force one translation to cover
// both, and whichever it picked would be wrong for the other.
func ParamKey(channelType, name string) string {
	return channelType + "." + name
}

// Params returns a language's parameter copy, or nil when it has none.
//
// English returns nil, and that is not a gap to be filled later: the English is
// the schema's own Label and Desc, which are the declaration rather than a copy
// of it. A second copy here would be a second place to update, and the second
// place is the one that goes stale.
func Params(l Lang) map[string]ParamCopy {
	if l == EN {
		return nil
	}
	return zhParams
}

// LookupParam finds one parameter's copy, reporting whether the table had it.
//
// Named LookupParam rather than Param because Param is already the query
// parameter that selects a language, and two things called Param in one package
// is a mistake waiting to be made by whoever writes the next call.
//
// False means "use what the schema says", which is English. That is the right
// degradation for a channel type added by somebody who has not translated it
// yet: the form still works, in one language, rather than showing a blank label
// where a field name should be. A test asserts it never fires for the types
// this build ships.
func LookupParam(l Lang, channelType, name string) (ParamCopy, bool) {
	table := Params(l)
	if table == nil {
		return ParamCopy{}, false
	}
	c, ok := table[ParamKey(channelType, name)]
	return c, ok
}
