// Hashes server specs to decide which launch resolutions stay valid.

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
)

// ComputeFingerprint hashes the parts of ServerSpec that, if changed,
// must invalidate any cached LaunchResolution. Platform + arch are
// folded in so the same source rebuilt on a different OS does not
// silently reuse a binary that only exists on the previous host.
//
// Note: ServerSpec.ID is intentionally NOT part of the input — the
// fingerprint is computed per-server, keyed externally by ServerID.
// A user editing a server's command should produce a *different*
// fingerprint even if they keep the same id.
func ComputeFingerprint(spec ServerSpec) string {
	payload := struct {
		TransportType string            `json:"transportType"`
		Command       string            `json:"command"`
		Args          []string          `json:"args"`
		Env           map[string]string `json:"env"`
		URL           string            `json:"url"`
		Headers       map[string]string `json:"headers"`
		Platform      string            `json:"platform"`
		Arch          string            `json:"arch"`
	}{
		TransportType: string(spec.Transport),
		Command:       spec.Command,
		Args:          spec.Args,
		Env:           spec.Env,
		URL:           spec.URL,
		Headers:       spec.Headers,
		Platform:      runtime.GOOS,
		Arch:          runtime.GOARCH,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
