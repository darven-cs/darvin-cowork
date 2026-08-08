// Tests for the output-capping writer.

package tool

import (
	"strings"
	"testing"
)

func TestLimitWriterUnderCap(t *testing.T) {
	w := &limitWriter{cap: 100}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 5 || w.Truncated() {
		t.Errorf("Len=%d Truncated=%v, want 5 false", w.Len(), w.Truncated())
	}
	if w.String() != "hello" {
		t.Errorf("String = %q, want %q", w.String(), "hello")
	}
}

func TestLimitWriterTruncatesAcrossWrites(t *testing.T) {
	w := &limitWriter{cap: 10}
	w.Write([]byte("0123456789"))
	if w.Truncated() {
		t.Error("exactly cap bytes should not report truncation")
	}
	w.Write([]byte("ABCDEF"))
	if !w.Truncated() {
		t.Error("overflow should report truncation")
	}
	if w.Len() != 16 {
		t.Errorf("Len = %d, want 16", w.Len())
	}
	if got := w.String(); got != "0123456789" {
		t.Errorf("String = %q, want first 10 bytes", got)
	}
}

func TestLimitWriterSingleLargeWrite(t *testing.T) {
	w := &limitWriter{cap: 5}
	big := strings.Repeat("x", 1000)
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	if !w.Truncated() || w.Len() != 1000 || len(w.String()) != 5 {
		t.Errorf("Truncated=%v Len=%d bufLen=%d, want true 1000 5", w.Truncated(), w.Len(), len(w.String()))
	}
}
