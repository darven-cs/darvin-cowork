// Redacts secret-shaped material so user credentials never leak into
// errors, logs, or the IPC wire.

package mcp

import (
	"regexp"
	"strings"
)

// secretKeyRe matches config keys that are likely to carry credentials.
// Applied case-insensitively to env / header keys before deciding whether
// to mask a value.
var secretKeyRe = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|apikey|auth|credential|bearer|authorization)`)

// bearerValueRe matches "Bearer <token>" as a unit, optionally preceded by
// an "Authorization:" prefix, so the whole header is masked in one pass.
var bearerValueRe = regexp.MustCompile(`(?i)((?:authorization|auth)?\s*\bbearer\b[=:\s]+)([A-Za-z0-9._\-]+)`)

// keyValueRe matches "<secret-ish key><connector><value>" in free text,
// e.g. "token=abc" / "api_key=xyz". Group 1 captures the original-cased
// key plus connector so replacement preserves case.
var keyValueRe = regexp.MustCompile(`(?i)((token|secret|password|passwd|api[_-]?key|apikey|auth|credential)[=:\s]+)([^\s,;"']{6,})`)

// urlUserinfoRe masks "scheme://user:password@host" userinfo blocks.
var urlUserinfoRe = regexp.MustCompile(`://[^/\s@]+:[^/\s@]+@`)

// RedactString replaces secret-shaped substrings in free text with "***".
// Used at the IPC wire boundary on error / stderr strings so a connection
// failure that echoes env or an Authorization header does not leak the
// value to the renderer or the agent log.
func RedactString(s string) string {
	if s == "" {
		return s
	}
	out := bearerValueRe.ReplaceAllString(s, "$1***")
	out = keyValueRe.ReplaceAllString(out, "$1***")
	out = urlUserinfoRe.ReplaceAllString(out, "://***:***@")
	return out
}

// IsSecretKey reports whether a config key (env name / header name) is
// likely to carry a credential. Header names are lower-cased for the
// comparison since HTTP headers are case-insensitive.
func IsSecretKey(key string) bool {
	return secretKeyRe.MatchString(key)
}

// RedactMap returns a copy of m with any value whose key looks
// secret-shaped replaced by "***". The map is not mutated.
func RedactMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSecretKey(k) {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}

// RedactSpec returns a copy of spec safe for display / IPC: env and
// headers values whose keys look secret-shaped are masked, and the URL is
// passed through RedactString in case it embeds credentials in userinfo.
func RedactSpec(s ServerSpec) ServerSpec {
	out := cloneSpec(s)
	if len(out.Env) > 0 {
		out.Env = RedactMap(out.Env)
	}
	if len(out.Headers) > 0 {
		out.Headers = RedactMap(out.Headers)
	}
	if out.URL != "" {
		out.URL = RedactString(out.URL)
	}
	if out.Command != "" {
		out.Command = RedactString(out.Command)
	}
	return out
}

// RedactResolution returns a copy of a LaunchResolution safe for the IPC
// wire: error / stderr fields are redacted as free text, env values are
// masked by key. Arguments may embed a token in a flag value; redact those
// too when the flag name looks secret-ish.
func RedactResolution(r LaunchResolution) LaunchResolution {
	out := r
	out.Error = RedactString(r.Error)
	out.FailureStderr = RedactString(r.FailureStderr)
	if len(out.Env) > 0 {
		out.Env = RedactMap(out.Env)
	}
	if len(out.Args) > 0 {
		redacted := make([]string, len(out.Args))
		// Manual index loop: a Go range loop re-assigns i from its own
		// counter each iteration, so the skip below must not rely on
		// mutating the range variable.
		i := 0
		for i < len(out.Args) {
			a := out.Args[i]
			// "--token abc" spans two argv entries; mask the value that
			// follows a secret-looking long flag.
			if isSecretFlagName(a) && i+1 < len(out.Args) {
				redacted[i] = a
				redacted[i+1] = "***"
				i += 2
				continue
			}
			redacted[i] = redactArg(a)
			i++
		}
		out.Args = redacted
	}
	return out
}

// redactArg masks the value of a secret-looking "--flag=value", and runs
// free-text redaction over the whole arg (catches URL userinfo, inline
// token=value, etc.).
func redactArg(a string) string {
	trimmed := strings.TrimSpace(a)
	if trimmed == "" {
		return a
	}
	if eq := strings.Index(trimmed, "="); eq > 0 {
		prefix := trimmed[:eq]
		if IsSecretKey(prefix) {
			return prefix + "=***"
		}
	}
	return RedactString(a)
}

// isSecretFlagName reports whether a long flag name looks secret-ish, e.g.
// "--token" / "--api-key" but not "-t" or "--registry".
func isSecretFlagName(a string) bool {
	trimmed := strings.TrimSpace(a)
	if !strings.HasPrefix(trimmed, "--") {
		return false
	}
	name := strings.TrimLeft(trimmed, "-")
	return name != "" && IsSecretKey(name)
}
