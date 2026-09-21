package api

import (
	"log/slog"
	"net/http"

	"notifyrelay/internal/channel"
	"notifyrelay/internal/message"
	"notifyrelay/internal/router"
)

// channelInfo describes one registered channel type.
//
// It carries enough to construct a valid configuration without reading the
// source: the parameter schema, which parameters are secret, and what the
// channel can render.
type channelInfo struct {
	Type       string              `json:"type"`
	Configured []string            `json:"configured_instances"`
	Parameters []channel.ParamSpec `json:"parameters"`
	Capability capabilityInfo      `json:"capability"`
}

type capabilityInfo struct {
	SupportedFormats  []string `json:"supported_formats"`
	BodyMaxLen        int      `json:"body_max_len"`
	TitleMaxLen       int      `json:"title_max_len"`
	SupportAttachment bool     `json:"support_attachment"`
	RatePerSec        float64  `json:"rate_per_sec"`
	OverflowMode      string   `json:"overflow_mode"`
}

type channelsResponse struct {
	Channels []channelInfo `json:"channels"`
}

type channelsHandler struct {
	router *router.Router
	log    *slog.Logger
}

// ServeHTTP implements GET /api/v1/channels.
func (h *channelsHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	// Which configured instances use each type, so an operator can see at a
	// glance which of the available channels this deployment actually uses.
	instancesByType := map[string][]string{}
	if h.router != nil {
		for _, name := range h.router.Instances() {
			t := h.router.TypeOf(name)
			instancesByType[t] = append(instancesByType[t], name)
		}
	}

	descriptors := channel.Descriptors()
	out := make([]channelInfo, 0, len(descriptors))

	for _, d := range descriptors {
		specs := make([]channel.ParamSpec, 0, len(d.ParamSchema))
		for _, s := range d.ParamSchema {
			specs = append(specs, publicSpec(s))
		}

		out = append(out, channelInfo{
			Type:       d.Type,
			Configured: instancesByType[d.Type],
			Parameters: specs,
			Capability: capabilityInfo{
				SupportedFormats:  formatNames(d.Capability.SupportedFormats),
				BodyMaxLen:        d.Capability.BodyMaxLen,
				TitleMaxLen:       d.Capability.TitleMaxLen,
				SupportAttachment: d.Capability.SupportAttachment,
				RatePerSec:        d.Capability.RatePerSec,
				OverflowMode:      d.Capability.OverflowMode.String(),
			},
		})
	}

	WriteJSON(w, http.StatusOK, channelsResponse{Channels: out})
}

// publicSpec strips anything that should not leave the process.
//
// A declared default for a secret parameter would be a secret in the
// configuration file, and this endpoint is not the place to publish it.
func publicSpec(s channel.ParamSpec) channel.ParamSpec {
	if s.Private {
		s.Default = nil
	}
	return s
}

func formatNames(formats []message.Format) []string {
	out := make([]string, 0, len(formats))
	for _, f := range formats {
		out = append(out, string(f))
	}
	return out
}
