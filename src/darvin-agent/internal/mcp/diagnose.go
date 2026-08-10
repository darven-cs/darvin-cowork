// Connection diagnostics: turns a failed connection + config into an
// actionable auth verdict (none / possible / required) plus a suggestion.

package mcp

import (
	"regexp"
	"strings"
)

// AuthStatus is the three-state auth verdict the renderer can act on.
type AuthStatus string

const (
	AuthNone     AuthStatus = "none"
	AuthPossible AuthStatus = "possible"
	AuthRequired AuthStatus = "required"
)

// AuthDiagnosis is the actionable output of DiagnoseAuth.
type AuthDiagnosis struct {
	Status     AuthStatus `json:"status"`
	Suggestion string     `json:"suggestion,omitempty"`
}

// authFailureRe matches error text that indicates authentication rejected
// the request. Mirrors Reasonix's mcpdiag auth-failure matcher.
var authFailureRe = regexp.MustCompile(`(?i)(401|403|unauthorized|forbidden|invalid token|login required|not authenticated|authentication failed|access denied)`)

// DiagnoseAuth classifies a connection failure. Transport type, whether the
// server carries explicit credentials, and the URL scheme decide whether the
// native MCP OAuth flow is even an option:
//
//   - required: the failure looks like an auth rejection AND the server is
//     reachable via the OAuth-capable path (streamable HTTP over https /
//     loopback, no explicit creds configured).
//   - possible: the server is not connected yet but nothing has hard-failed
//     (deferred / initializing), so auth may still be needed later.
//   - none: everything else — either not an auth problem, or the transport
//     cannot do native OAuth (stdio / SSE / creds already set).
func DiagnoseAuth(transport TransportType, connected bool, errText string, url string, hasExplicitCreds bool) AuthDiagnosis {
	failed := !connected && strings.TrimSpace(errText) != ""
	if failed && authFailureRe.MatchString(errText) {
		if oauthEligible(transport, url, hasExplicitCreds) {
			return AuthDiagnosis{
				Status:     AuthRequired,
				Suggestion: "server requires OAuth authorization; open the authorize flow, or configure an access token in headers",
			}
		}
		return AuthDiagnosis{
			Status:     AuthNone,
			Suggestion: "server rejected credentials; check the token in headers/env, or the transport does not support native OAuth",
		}
	}
	if !connected {
		return AuthDiagnosis{
			Status:     AuthPossible,
			Suggestion: "server is not connected yet; authorization may be needed once it responds",
		}
	}
	return AuthDiagnosis{Status: AuthNone}
}

// oauthEligible reports whether native MCP OAuth is possible for this
// server: streamable HTTP, a secure-or-loopback URL, and no explicit
// credentials already configured.
func oauthEligible(t TransportType, url string, hasExplicitCreds bool) bool {
	if t != TransportHTTP || hasExplicitCreds {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(url))
	if strings.HasPrefix(u, "https://") {
		return true
	}
	// loopback http (localhost / 127.0.0.1) is acceptable for OAuth flows.
	return strings.HasPrefix(u, "http://localhost") || strings.HasPrefix(u, "http://127.0.0.1") || strings.HasPrefix(u, "http://[::1]")
}

// HasExplicitCredentials reports whether the server config carries a
// secret-shaped env / header entry that would pre-empt the OAuth flow.
func HasExplicitCredentials(s ServerSpec) bool {
	for k := range s.Env {
		if IsSecretKey(k) {
			return true
		}
	}
	for k := range s.Headers {
		if IsSecretKey(k) {
			return true
		}
	}
	return false
}
