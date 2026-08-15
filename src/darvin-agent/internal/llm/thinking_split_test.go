// Tests for the streaming <think> block splitter.

package llm_test

import (
	"reflect"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

// collectEvents feeds every delta into a fresh splitter then flushes it.
func collectEvents(deltas []string) []string {
	s := llm.NewThinkingSplitter()
	var out []llm.StreamEvent
	for _, d := range deltas {
		out = append(out, s.Feed(d)...)
	}
	out = append(out, s.Flush()...)
	return flatten(out)
}

func flatten(events []llm.StreamEvent) []string {
	var out []string
	for _, ev := range events {
		switch e := ev.(type) {
		case llm.TextDeltaEvent:
			out = append(out, "text:"+e.Delta)
		case llm.ThinkingDeltaEvent:
			out = append(out, "think:"+e.Delta)
		default:
			out = append(out, "other")
		}
	}
	return out
}

func TestThinkingSplitter(t *testing.T) {
	cases := []struct {
		name   string
		deltas []string
		want   []string
	}{
		{
			name:   "single block",
			deltas: []string{"<think>X</think>Y"},
			want:   []string{"think:X", "text:Y"},
		},
		{
			name:   "multiple blocks interleaved",
			deltas: []string{"<think>A</think>m<think>B</think>n"},
			want:   []string{"think:A", "text:m", "think:B", "text:n"},
		},
		{
			name:   "thinking variant",
			deltas: []string{"<thinking>X</thinking>Y"},
			want:   []string{"think:X", "text:Y"},
		},
		{
			name:   "empty think block",
			deltas: []string{"<think></think>Y"},
			want:   []string{"text:Y"},
		},
		{
			name:   "open tag split across deltas",
			deltas: []string{"<thi", "nk>X</think>Y"},
			want:   []string{"think:X", "text:Y"},
		},
		{
			name:   "thinking tag split across deltas",
			deltas: []string{"<thinki", "ng>X</thinkin", "g>Y"},
			want:   []string{"think:X", "text:Y"},
		},
		{
			name:   "content split across deltas",
			deltas: []string{"<think>ab", "cd</think>rest"},
			want:   []string{"think:ab", "think:cd", "text:rest"},
		},
		{
			name:   "close tag split across deltas",
			deltas: []string{"<think>abc</thi", "nk>rest"},
			want:   []string{"think:abc", "text:rest"},
		},
		{
			name:   "unclosed think block",
			deltas: []string{"<think>abc"},
			want:   []string{"think:abc"},
		},
		{
			name:   "plain text passes through",
			deltas: []string{"hello world"},
			want:   []string{"text:hello world"},
		},
		{
			name:   "empty delta no-op",
			deltas: []string{"", "<think>X</think>"},
			want:   []string{"think:X"},
		},
		{
			name:   "suspicious suffix held then released as text",
			deltas: []string{"abc<thi", "s is literal</think>text"},
			want:   []string{"text:abc", "text:<this is literal</think>text"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectEvents(tc.deltas)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %v\nwant %v", got, tc.want)
			}
		})
	}
}
