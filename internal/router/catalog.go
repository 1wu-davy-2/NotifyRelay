package router

import (
	"sort"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
)

// CatalogEntry describes one registered channel type.
//
// It carries enough to construct a valid configuration without reading the
// source: the parameter schema, which parameters are secret, which are
// conditional on another parameter, what bounds they have, and what the channel
// can render.
//
// It lives here rather than in the HTTP layer because it has two consumers with
// nothing else in common — the bearer-authenticated /api/v1/channels document
// and the operator UI's form generator — and a second copy of it would be a
// second thing to keep in step with ParamSpec.
type CatalogEntry struct {
	Type string `json:"type"`
	// Configured lists the instance aliases using this type, sorted, so an
	// operator can see at a glance which of the available channels this
	// deployment actually uses.
	Configured []string            `json:"configured_instances"`
	Parameters []channel.ParamSpec `json:"parameters"`
	Capability CatalogCapability   `json:"capability"`
}

// CatalogCapability is the machine-readable half of a channel's declaration.
type CatalogCapability struct {
	SupportedFormats  []string `json:"supported_formats"`
	BodyMaxLen        int      `json:"body_max_len"`
	BodyMaxBytes      int      `json:"body_max_bytes"`
	TitleMaxLen       int      `json:"title_max_len"`
	SupportAttachment bool     `json:"support_attachment"`
	RatePerSec        float64  `json:"rate_per_sec"`
	OverflowMode      string   `json:"overflow_mode"`
	MarkdownDialect   string   `json:"markdown_dialect"`
}

// Catalog describes every registered channel type.
//
// r may be nil, for a caller that wants the catalogue before any channels are
// configured — which is exactly the situation of somebody reading it in order
// to write the configuration.
func Catalog(r *Router) []CatalogEntry {
	instancesByType := map[string][]string{}
	if r != nil {
		for _, name := range r.Instances() {
			t := r.TypeOf(name)
			instancesByType[t] = append(instancesByType[t], name)
		}
	}

	descriptors := channel.Descriptors()
	out := make([]CatalogEntry, 0, len(descriptors))

	for _, d := range descriptors {
		// A private parameter's declared default is stripped: a default for a
		// secret is a secret in the configuration file, and this document is
		// not the place to publish it.
		specs := make([]channel.ParamSpec, 0, len(d.ParamSchema))
		for _, s := range d.ParamSchema {
			if s.Private {
				s.Default = nil
			}
			specs = append(specs, s)
		}

		configured := instancesByType[d.Type]
		sort.Strings(configured)

		out = append(out, CatalogEntry{
			Type:       d.Type,
			Configured: configured,
			Parameters: specs,
			Capability: CatalogCapability{
				SupportedFormats:  formatNames(d.Capability.SupportedFormats),
				BodyMaxLen:        d.Capability.BodyMaxLen,
				BodyMaxBytes:      d.Capability.BodyMaxBytes,
				TitleMaxLen:       d.Capability.TitleMaxLen,
				SupportAttachment: d.Capability.SupportAttachment,
				RatePerSec:        d.Capability.RatePerSec,
				OverflowMode:      d.Capability.OverflowMode.String(),
				MarkdownDialect:   string(d.Capability.MarkdownDialect),
			},
		})
	}

	return out
}

func formatNames(formats []message.Format) []string {
	out := make([]string, 0, len(formats))
	for _, f := range formats {
		out = append(out, string(f))
	}
	return out
}
