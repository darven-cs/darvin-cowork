// Tests for OpenAI SSE parsing and stream dispatch.

package openai

import (
	"context"
	"strings"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

// collectStream feeds raw SSE text through runStream and returns the events.
func collectStream(t *testing.T, sse string) []llm.StreamEvent {
	t.Helper()
	out := make(chan llm.StreamEvent, 64)
	err := runStream(context.Background(), strings.NewReader(sse), out, "gpt-4o")
	if err != nil {
		t.Fatalf("runStream: %v", err)
	}
	close(out)
	var events []llm.StreamEvent
	for ev := range out {
		events = append(events, ev)
	}
	return events
}

func TestStream_Text(t *testing.T) {
	events := collectStream(t, `data: {"id":"1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}

data: [DONE]
`)
	var text string
	var done bool
	for _, ev := range events {
		switch e := ev.(type) {
		case llm.TextDeltaEvent:
			text += e.Delta
		case llm.DoneEvent:
			done = true
			if e.Response.Content != "Hello world" {
				t.Errorf("content = %q, want %q", e.Response.Content, "Hello world")
			}
			if e.Response.FinishReason != llm.FinishReasonStop {
				t.Errorf("finish_reason = %v, want stop", e.Response.FinishReason)
			}
		}
	}
	if !done {
		t.Fatal("missing DoneEvent")
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
}

func TestStream_ReasoningPassthrough(t *testing.T) {
	events := collectStream(t, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"think step"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}

data: [DONE]
`)
	var reasoning, text string
	for _, ev := range events {
		switch e := ev.(type) {
		case llm.ThinkingDeltaEvent:
			reasoning += e.Delta
		case llm.TextDeltaEvent:
			text += e.Delta
		}
	}
	if reasoning != "think step" {
		t.Errorf("reasoning = %q, want %q", reasoning, "think step")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

func TestStream_ToolCall(t *testing.T) {
	events := collectStream(t, `data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":""}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"mcp\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
`)
	var start, end int
	var argJSON string
	var done bool
	for _, ev := range events {
		switch e := ev.(type) {
		case llm.ToolCallStartEvent:
			start++
			if e.ID != "call_1" || e.Name != "search" {
				t.Errorf("start = %+v, want call_1/search", e)
			}
		case llm.ToolCallDeltaEvent:
			argJSON += e.Delta
		case llm.ToolCallEndEvent:
			end++
			if e.ID != "call_1" || e.Name != "search" {
				t.Errorf("end = %+v, want call_1/search", e)
			}
			if e.Arguments["q"] != "mcp" {
				t.Errorf("args = %v, want q=mcp", e.Arguments)
			}
		case llm.DoneEvent:
			done = true
			if e.Response.FinishReason != llm.FinishReasonToolCalls {
				t.Errorf("finish_reason = %v, want tool_calls", e.Response.FinishReason)
			}
		}
	}
	if !done {
		t.Fatal("missing DoneEvent")
	}
	if start != 1 || end != 1 {
		t.Errorf("start=%d end=%d, want 1/1", start, end)
	}
	if argJSON != `{"q":"mcp"}` {
		t.Errorf("argJSON = %q", argJSON)
	}
}

func TestStream_UsageInFinalChunk(t *testing.T) {
	events := collectStream(t, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":2,"total_tokens":13}}

data: [DONE]
`)
	var done bool
	for _, ev := range events {
		if e, ok := ev.(llm.DoneEvent); ok {
			done = true
			if e.Response.Usage.PromptTokens != 11 || e.Response.Usage.CompletionTokens != 2 {
				t.Errorf("usage = %+v, want 11/2", e.Response.Usage)
			}
		}
	}
	if !done {
		t.Fatal("missing DoneEvent")
	}
}

func TestParseToolArgs(t *testing.T) {
	if got := parseToolArgs(""); len(got) != 0 {
		t.Errorf("empty => %+v, want empty", got)
	}
	got := parseToolArgs(`{"a":1}`)
	if got["a"] != float64(1) {
		t.Errorf("got %+v", got)
	}
	if got := parseToolArgs("nope"); got["_raw"] != "nope" {
		t.Errorf("invalid => %+v, want _raw fallback", got)
	}
}
