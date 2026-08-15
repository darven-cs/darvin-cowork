// Streaming splitter that extracts <think>/<thinking> blocks from provider
// text deltas and routes the inner content to ThinkingDeltaEvent. Reasoning
// emitted as inline tags (DeepSeek-R1 style) is kept out of visible text.

package llm

import "strings"

var (
	openTags  = []string{"<think>", "<thinking>"}
	closeTags = []string{"</think>", "</thinking>"}
)

// ThinkingSplitter is a streaming state machine that recognises
// <think>...</think> / <thinking>...</thinking> blocks split across any
// number of text deltas. Feed returns the events to emit for each delta;
// Flush returns residual events when the stream ends.
type ThinkingSplitter struct {
	inThink bool
	pending strings.Builder
	think   strings.Builder
}

// NewThinkingSplitter returns an idle splitter.
func NewThinkingSplitter() *ThinkingSplitter {
	return &ThinkingSplitter{}
}

// Feed consumes one text delta and returns the events to emit in order.
func (s *ThinkingSplitter) Feed(delta string) []StreamEvent {
	if delta == "" {
		return nil
	}
	var out []StreamEvent
	if s.inThink {
		s.feedThink(delta, &out)
	} else {
		s.feedNormal(delta, &out)
	}
	return out
}

// Flush emits residual content when the stream ends: an unclosed trailing
// think block is treated as thinking, leftover normal text as text.
func (s *ThinkingSplitter) Flush() []StreamEvent {
	var out []StreamEvent
	if s.inThink {
		if t := s.think.String(); t != "" {
			out = append(out, ThinkingDeltaEvent{Delta: t})
		}
		s.think.Reset()
	} else if p := s.pending.String(); p != "" {
		out = append(out, TextDeltaEvent{Delta: p})
		s.pending.Reset()
	}
	return out
}

func (s *ThinkingSplitter) feedNormal(delta string, out *[]StreamEvent) {
	s.pending.WriteString(delta)
	m := findEarliestTag(s.pending.String(), openTags)
	if m.start < 0 {
		emitHeldText(s, out)
		return
	}
	if m.start > 0 {
		*out = append(*out, TextDeltaEvent{Delta: s.pending.String()[:m.start]})
	}
	s.think.Reset()
	s.think.WriteString(s.pending.String()[m.end:])
	s.pending.Reset()
	s.inThink = true
	s.processThink(out)
}

func (s *ThinkingSplitter) feedThink(delta string, out *[]StreamEvent) {
	s.think.WriteString(delta)
	s.processThink(out)
}

// processThink scans the think accumulator for a closing tag. A complete
// close emits the inner content as ThinkingDeltaEvent and returns to normal
// mode; a partial tag at the tail is held back; otherwise content is emitted
// immediately so long streams stream out chunk by chunk.
func (s *ThinkingSplitter) processThink(out *[]StreamEvent) {
	m := findEarliestTag(s.think.String(), closeTags)
	if m.start < 0 {
		emitHeldThink(s, out)
		return
	}
	if m.start > 0 {
		*out = append(*out, ThinkingDeltaEvent{Delta: s.think.String()[:m.start]})
	}
	remainder := s.think.String()[m.end:]
	s.think.Reset()
	s.inThink = false
	if remainder != "" {
		s.feedNormal(remainder, out)
	}
}

// emitHeldText emits the safe prefix of pending normal-mode text and keeps
// only a suffix that could be the start of an opening tag.
func emitHeldText(s *ThinkingSplitter, out *[]StreamEvent) {
	susp := longestSuspiciousSuffix(s.pending.String(), openTags)
	text := s.pending.String()
	s.pending.Reset()
	if cut := len(text) - len(susp); cut > 0 {
		*out = append(*out, TextDeltaEvent{Delta: text[:cut]})
	}
	s.pending.WriteString(susp)
}

// emitHeldThink emits the safe prefix of accumulated thinking content and
// keeps only a suffix that could be the start of a closing tag.
func emitHeldThink(s *ThinkingSplitter, out *[]StreamEvent) {
	susp := longestSuspiciousSuffix(s.think.String(), closeTags)
	text := s.think.String()
	s.think.Reset()
	if cut := len(text) - len(susp); cut > 0 {
		*out = append(*out, ThinkingDeltaEvent{Delta: text[:cut]})
	}
	s.think.WriteString(susp)
}

// tagMatch locates the earliest occurrence of any tag in a string.
type tagMatch struct {
	start int
	end   int
}

// findEarliestTag returns the earliest match of any tag in s, or {-1, 0}
// when no tag is present.
func findEarliestTag(s string, tags []string) tagMatch {
	best := tagMatch{start: -1}
	for _, tag := range tags {
		if idx := strings.Index(s, tag); idx >= 0 && (best.start < 0 || idx < best.start) {
			best = tagMatch{start: idx, end: idx + len(tag)}
		}
	}
	return best
}

// longestSuspiciousSuffix returns the longest suffix of s that is a strict
// prefix of some tag (a tag still being streamed in). Empty means no suffix
// is worth holding back.
func longestSuspiciousSuffix(s string, tags []string) string {
	for n := len(s); n > 0; n-- {
		suffix := s[len(s)-n:]
		for _, tag := range tags {
			if len(suffix) < len(tag) && strings.HasPrefix(tag, suffix) {
				return suffix
			}
		}
	}
	return ""
}
