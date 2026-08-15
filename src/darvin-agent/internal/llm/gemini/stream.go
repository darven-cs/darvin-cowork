// Parses Gemini streamGenerateContent SSE chunks into the unified StreamEvent.

package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"darvin-cowork/backend/internal/llm"
)

// streamChunk is one Gemini SSE data payload. Tool calls arrive fully
// formed in a single chunk (no incremental deltas), unlike OpenAI.
type streamChunk struct {
	Candidates []struct {
		Content struct {
			Role  string `json:"role"`
			Parts []struct {
				Text         string        `json:"text"`
				FunctionCall *functionCall `json:"functionCall"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// runStream scans a Gemini SSE body, dispatching each chunk to the unified
// channel. Text parts map to TextDeltaEvent; functionCall parts map to a
// ToolCallStart + ToolCallEnd pair (Gemini delivers calls atomically). The
// terminal DoneEvent is synthesised after the stream drains.
func runStream(ctx context.Context, r io.Reader, out chan<- llm.StreamEvent, model string) error {
	var (
		finishReason   string
		usage          llm.Usage
		respModel      = model
		startEmitted   bool
		partialContent strings.Builder
		splitter       = llm.NewThinkingSplitter()
	)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataBuf strings.Builder
	flush := func() error {
		if dataBuf.Len() == 0 {
			return nil
		}
		raw := dataBuf.String()
		dataBuf.Reset()
		return dispatch(raw, &dispatchState{
			model:        model,
			respModel:    &respModel,
			usage:        &usage,
			finishReason: &finishReason,
			startEmitted: &startEmitted,
			partial:      &partialContent,
			splitter:     splitter,
			out:          out,
		})
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		case strings.HasPrefix(line, ":"):
			continue
		default:
			continue
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ctx.Err()
		}
		return llm.NewProviderError("gemini", llm.ErrCodeInternal,
			fmt.Sprintf("sse scan: %s", err.Error()), 0, err)
	}
	if !startEmitted {
		select {
		case out <- llm.StartEvent{Partial: llm.AssistantMessage{Model: respModel}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Flush residual thinking/text before the terminal event so a trailing
	// unclosed <think> still reaches the client as thinking.
	for _, ev := range splitter.Flush() {
		if te, ok := ev.(llm.TextDeltaEvent); ok {
			partialContent.WriteString(te.Delta)
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	select {
	case out <- llm.DoneEvent{Response: llm.CompletionResponse{
		Model:        respModel,
		Content:      partialContent.String(),
		FinishReason: mapFinishReason(finishReason),
		Usage:        usage,
	}}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

type dispatchState struct {
	model        string
	respModel    *string
	usage        *llm.Usage
	finishReason *string
	startEmitted *bool
	partial      *strings.Builder
	splitter     *llm.ThinkingSplitter
	out          chan<- llm.StreamEvent
}

func dispatch(raw string, st *dispatchState) error {
	var c streamChunk
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// Forward-compatible garbage: skip rather than abort the stream.
		return nil
	}
	if c.UsageMetadata.PromptTokenCount > 0 || c.UsageMetadata.CandidatesTokenCount > 0 {
		st.usage.PromptTokens = c.UsageMetadata.PromptTokenCount
		st.usage.CompletionTokens = c.UsageMetadata.CandidatesTokenCount
		st.usage.TotalTokens = c.UsageMetadata.TotalTokenCount
	}
	if len(c.Candidates) == 0 {
		return nil
	}
	ch := c.Candidates[0]
	if !*st.startEmitted {
		*st.startEmitted = true
		select {
		case st.out <- llm.StartEvent{Partial: llm.AssistantMessage{Model: *st.respModel}}:
		default:
		}
	}
	for _, p := range ch.Content.Parts {
		if p.FunctionCall != nil {
			args := p.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			// Gemini correlates tool results by name; use the name as the id.
			st.out <- llm.ToolCallStartEvent{ID: p.FunctionCall.Name, Name: p.FunctionCall.Name}
			st.out <- llm.ToolCallEndEvent{ID: p.FunctionCall.Name, Name: p.FunctionCall.Name, Arguments: args}
			continue
		}
		if p.Text != "" {
			for _, ev := range st.splitter.Feed(p.Text) {
				if te, ok := ev.(llm.TextDeltaEvent); ok {
					st.partial.WriteString(te.Delta)
				}
				st.out <- ev
			}
		}
	}
	if ch.FinishReason != "" {
		*st.finishReason = ch.FinishReason
	}
	return nil
}
