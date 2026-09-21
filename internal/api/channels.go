package api

import (
	"log/slog"
	"net/http"

	"notifyrelay/internal/router"
)

type channelsResponse struct {
	Channels []router.CatalogEntry `json:"channels"`
}

type channelsHandler struct {
	router *router.Router
	log    *slog.Logger
}

// ServeHTTP implements GET /api/v1/channels.
//
// The document itself is built by router.Catalog, which the operator UI also
// uses: the two surfaces differ in how they are authenticated, not in what they
// say a channel is.
func (h *channelsHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, channelsResponse{Channels: router.Catalog(h.router)})
}
