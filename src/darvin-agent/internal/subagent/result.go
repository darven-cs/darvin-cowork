// result.go: byte-offset pagination for sub-agent result reads.

package subagent

// DefaultPageSize is the byte count returned by ReadResult with limit=0.
const DefaultPageSize = 12 * 1024

// MaxPageSize caps the page size a caller may request.
const MaxPageSize = 24 * 1024

// Paginate returns the substring of `text` starting at `offset` and
// bounded by `limit`. limit<=0 falls back to DefaultPageSize; limit is
// then clamped to MaxPageSize. offset beyond end returns "".
func Paginate(text string, offset, limit int) string {
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(text) {
		return ""
	}
	end := offset + limit
	if end > len(text) {
		end = len(text)
	}
	return text[offset:end]
}
