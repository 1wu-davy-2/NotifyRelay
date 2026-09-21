// Package requestid generates correlation identifiers.
//
// One identifier spans every delivery a single inbound request produces, so
// the audit trail of a fan-out can be reassembled from the log.
package requestid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// New returns a short random identifier.
func New() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; the timestamp keeps the
		// function total rather than returning an empty correlation key.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
