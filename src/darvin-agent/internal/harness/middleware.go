// Provides result middleware that normalises tool output size and error text.

package harness

import (
	"strings"

	"darvin-cowork/backend/internal/agents/protocol"
)

const (
	// DefaultMaxResultBytes is the size cap a tool result is normalised to
	// by DefaultMiddleware. Sized so a multi-turn conversation does not blow
	// the LLM context with a single tool call.
	DefaultMaxResultBytes = 50 * 1024
	// DefaultMaxResultLines caps the number of lines DefaultMiddleware
	// allows through.
	DefaultMaxResultLines = 2000
	// errorScanBytes is the prefix length NormalizeError inspects when
	// guessing whether Content describes an error. Bounded so a 50KB text
	// payload does not pay a full scan on every tool call.
	errorScanBytes = 1024
)

// MaxResultBytes caps result.Content at maxBytes. Oversize content is
// truncated and suffixed with a [truncated N bytes] marker; Metadata
// "truncated" is set to the truncated byte count so downstream consumers
// can detect the cut.
func MaxResultBytes(maxBytes int) ResultMiddleware {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResultBytes
	}
	return func(r protocol.Result) protocol.Result {
		if len(r.Content) <= maxBytes {
			return r
		}
		meta := cloneMetadata(r.Metadata)
		if meta == nil {
			meta = map[string]any{}
		}
		cut := maxBytes
		meta["truncated"] = len(r.Content) - maxBytes
		return protocol.Result{
			Content:  r.Content[:cut] + "[truncated " + itoa(len(r.Content)-maxBytes) + " bytes]",
			IsError:  r.IsError,
			Metadata: meta,
		}
	}
}

// MaxResultLines caps the number of lines. Lines beyond maxLines are
// dropped and the result is suffixed with the marker; Metadata
// "line_truncated" is set to the dropped line count.
func MaxResultLines(maxLines int) ResultMiddleware {
	if maxLines <= 0 {
		maxLines = DefaultMaxResultLines
	}
	return func(r protocol.Result) protocol.Result {
		lines := strings.Split(r.Content, "\n")
		if len(lines) <= maxLines {
			return r
		}
		kept := strings.Join(lines[:maxLines], "\n")
		meta := cloneMetadata(r.Metadata)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["line_truncated"] = len(lines) - maxLines
		return protocol.Result{
			Content:  kept + "[truncated " + itoa(len(lines)-maxLines) + " lines]",
			IsError:  r.IsError,
			Metadata: meta,
		}
	}
}

// NormalizeError flips IsError=true on results whose Content starts with a
// known error prefix. Backends that surface errors through Content rather
// than the IsError flag — most commonly CLI subprocesses — become consistent
// with the embedded runtime after this pass.
func NormalizeError() ResultMiddleware {
	return func(r protocol.Result) protocol.Result {
		if r.IsError {
			return r
		}
		prefix := r.Content
		if len(prefix) > errorScanBytes {
			prefix = prefix[:errorScanBytes]
		}
		if !looksLikeError(prefix) {
			return r
		}
		return protocol.Result{
			Content:  "[error] " + strings.TrimSpace(r.Content),
			IsError:  true,
			Metadata: r.Metadata,
		}
	}
}

// SanitizeControlChars replaces NUL and other control characters (except
// tab / LF / CR, which the JSON formatter and the LLM both rely on) with a
// single space, so a malformed tool payload cannot break protocol parsing.
func SanitizeControlChars() ResultMiddleware {
	return func(r protocol.Result) protocol.Result {
		if !containsControl(r.Content) {
			return r
		}
		var b strings.Builder
		b.Grow(len(r.Content))
		for _, c := range r.Content {
			if isDangerousControl(c) {
				b.WriteByte(' ')
			} else {
				b.WriteRune(c)
			}
		}
		return protocol.Result{Content: b.String(), IsError: r.IsError, Metadata: r.Metadata}
	}
}

// WithToolMetadata stamps toolName and kind onto Metadata. The executor
// already attaches these on tool_end events; this is a fallback for paths
// that bypass the executor (e.g. a CLI harness that runs tools out of band).
func WithToolMetadata(toolName string, kind protocol.Kind) ResultMiddleware {
	return func(r protocol.Result) protocol.Result {
		meta := cloneMetadata(r.Metadata)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["tool_name"] = toolName
		meta["tool_kind"] = string(kind)
		return protocol.Result{Content: r.Content, IsError: r.IsError, Metadata: meta}
	}
}

// DefaultMiddleware is the standard chain the embedded harness should run.
// Order matters: sanitise first so a 50KB content scan never sees a stray
// NUL, then convert CLI-style error text into IsError so the size cap
// downstream applies the error prefix rather than the content.
func DefaultMiddleware() []ResultMiddleware {
	return []ResultMiddleware{
		SanitizeControlChars(),
		NormalizeError(),
		MaxResultBytes(DefaultMaxResultBytes),
		MaxResultLines(DefaultMaxResultLines),
	}
}

// Chain composes mws into one ResultMiddleware. The first middleware in
// the argument list runs last.
func Chain(mws ...ResultMiddleware) ResultMiddleware {
	if len(mws) == 0 {
		return identity
	}
	out := make([]ResultMiddleware, len(mws))
	copy(out, mws)
	return func(r protocol.Result) protocol.Result {
		cur := r
		for i := len(out) - 1; i >= 0; i-- {
			cur = out[i](cur)
		}
		return cur
	}
}

func identity(r protocol.Result) protocol.Result { return r }

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func looksLikeError(prefix string) bool {
	p := strings.TrimLeft(prefix, " \t")
	return strings.HasPrefix(p, "error:") ||
		strings.HasPrefix(p, "Error:") ||
		strings.HasPrefix(p, "ERROR:")
}

func containsControl(s string) bool {
	for _, c := range s {
		if isDangerousControl(c) {
			return true
		}
	}
	return false
}

// isDangerousControl reports whether c is a control character the LLM
// pipeline should not see. Tab / LF / CR are excluded because JSON strings
// and the renderer both depend on them.
func isDangerousControl(c rune) bool {
	switch {
	case c == 0x09, c == 0x0A, c == 0x0D:
		return false
	case c < 0x20:
		return true
	default:
		return false
	}
}

// itoa formats a non-negative integer without dragging in fmt to keep this
// file free of allocation-heavy formatters.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
