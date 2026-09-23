package admin

import (
	"net/http"
)

// Onboarding is the four steps between a fresh deployment and a notification
// that has actually arrived.
//
// It is derived from what the deployment contains rather than stored as a
// checklist, and that is the whole design. A stored checklist can be wrong —
// somebody deletes their only channel and the page still says the step is done
// — and a progress page that lies is worse than no progress page. Every field
// here is a fact about the service, asked at the moment the page is rendered.
//
// It also costs nothing to keep: the fourth step is the one an operator
// actually wants, and the third is the moment they stop guessing.
type Onboarding struct {
	HasChannel  bool
	HasKey      bool
	HasDelivery bool
	HasResult   bool
}

// Done reports whether every step is finished. The navigation stops offering
// the checklist once it is.
func (o Onboarding) Done() bool {
	return o.HasChannel && o.HasKey && o.HasDelivery && o.HasResult
}

// Remaining counts the steps still to do, for the count beside the nav item.
func (o Onboarding) Remaining() int {
	n := 0
	for _, done := range []bool{o.HasChannel, o.HasKey, o.HasDelivery, o.HasResult} {
		if !done {
			n++
		}
	}
	return n
}

// onboarding asks the deployment which steps are done.
//
// Three small reads on every page render, because the navigation has to know
// whether to offer the checklist at all. That is more than a badge usually
// costs and it is the right trade here: the alternative is a flag in the meta
// table, and a flag is a page that keeps saying "3 of 4" long after the
// operator finished all four.
//
// A store that cannot answer is treated as "nothing done", which hides the
// checklist rather than showing a wrong one. The steps are a convenience; a
// page that reports progress it cannot verify is not.
func (h *handler) onboarding(r *http.Request) Onboarding {
	ctx := r.Context()
	var out Onboarding

	if h.deps.Channels != nil {
		if stored, err := h.deps.Channels.Load(ctx); err == nil {
			out.HasChannel = len(stored) > 0
		}
	}

	// keyViews rather than the key store directly: a key can also come from the
	// configuration file, and a deployment that was handed one has finished
	// this step whether or not anything is in the database.
	if h.deps.Keys != nil || len(h.configuredKeyNames()) > 0 {
		if keys, err := h.keyViews(r); err == nil {
			out.HasKey = len(keys) > 0
		}
	}

	if h.deps.Deliveries != nil {
		if stats, err := h.deps.Deliveries.Stats(ctx); err == nil {
			// Queued or sending counts for the third step: the notification has
			// been sent, and what happens next is the fourth step's business.
			out.HasDelivery = stats.Pending()+stats.Sent+stats.Failed > 0
			// The fourth step is "look at what happened", not "it worked". A
			// test that failed is a result, and it is the one that tells the
			// operator something.
			out.HasResult = stats.Sent+stats.Failed > 0
		}
	}

	return out
}

// onboardingResponse is the checklist's state, as the client-side interface
// reads it.
//
// A separate type rather than JSON tags on Onboarding, because that struct's
// four fields are the whole state and these six are a projection of it: Done
// and Remaining are methods there, and a client left to derive them from the
// booleans would be a second copy of the rule deciding what counts as finished.
// The arithmetic is trivial; the reason to send it is that the day a fifth step
// is added, the rule changes in one place.
type onboardingResponse struct {
	HasChannel  bool `json:"has_channel"`
	HasKey      bool `json:"has_key"`
	HasDelivery bool `json:"has_delivery"`
	HasResult   bool `json:"has_result"`
	Done        bool `json:"done"`
	Remaining   int  `json:"remaining"`
}

// onboardingJSON implements GET /admin/api/onboarding.
//
// The client-side interface needs this on every page, because the navigation
// offers the checklist only while something is left to do — the same reason the
// server-rendered shell computes it on every render. It is one endpoint rather
// than three (channels, keys, stats) so that the derivation above stays in one
// place and the page makes one request instead of three.
func (h *handler) onboardingJSON(w http.ResponseWriter, r *http.Request) {
	o := h.onboarding(r)
	writeJSON(w, http.StatusOK, onboardingResponse{
		HasChannel:  o.HasChannel,
		HasKey:      o.HasKey,
		HasDelivery: o.HasDelivery,
		HasResult:   o.HasResult,
		Done:        o.Done(),
		Remaining:   o.Remaining(),
	})
}
