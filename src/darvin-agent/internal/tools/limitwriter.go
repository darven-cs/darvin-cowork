package tool

import "bytes"

// limitWriter is an io.Writer that retains only the first cap bytes of
// everything written to it. Bytes beyond cap are discarded but still
// counted, so callers can detect truncation via Truncated().
type limitWriter struct {
	cap       int64
	buf       bytes.Buffer
	written   int64
	truncated bool
}

// Write appends up to cap bytes to the buffer and reports truncation when
// the stream exceeds cap. The returned n is the full input length — an
// io.Writer contract that keeps exec's stdout copy loop happy. Len() counts
// every attempted byte, including those discarded past cap.
func (w *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.written += int64(n)
	room := w.cap - int64(w.buf.Len())
	if room > 0 {
		if int64(n) > room {
			w.buf.Write(p[:int(room)])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if n > 0 {
		w.truncated = true
	}
	return n, nil
}

// Len returns the total number of bytes attempted to be written.
func (w *limitWriter) Len() int64 { return w.written }

// Truncated reports whether the stream exceeded cap.
func (w *limitWriter) Truncated() bool { return w.truncated }

// String returns the retained (bounded) content.
func (w *limitWriter) String() string { return w.buf.String() }
