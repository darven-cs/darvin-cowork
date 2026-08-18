// Small shared helpers for the im package (id minting and list formatting).

package im

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// newID mints a URL-friendly unique id for a new instance / QR session.
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// joinList serializes an allowlist slice to the comma-joined store form.
func joinList(items []string) string {
	return strings.Join(items, ",")
}
