// Parses OpenAI chat.completions SSE chunks into the unified StreamEvent.

package openai

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

// toolAccum tracks per-index tool call state during streaming so we can emit
// a single ToolCallStart / multiple ToolCallDelta / single ToolCallEnd triplet
// per invocation, matching the unified StreamEvent contract.
type toolAccum struct {
	id        string
	name      string
	arguments strings.Builder
	started   bool
}

// openStream opens a streaming call and returns the unified StreamingResponse.
func openStream(
	ctx context.Context,
	hc *httpClient,
	url string,
	headers map[string]string,
	req *llm.CompletionRequest,
) (*llm.StreamingResponse, error) {
	payload, err := buildRequest(req, true)
	if err != nil {
		return nil, err
	}
	body, err := hc.DoStream(ctx, providerName(req), url, headers, payload)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent, 16)
	sr := llm.NewStreamingResponse(events, body)

	go func() {
		defer close(events)
		defer body.Close()
		if err := runStream(ctx, body, events, req.Model); err != nil {
			sr.SetErr(err)
			select {
			case events <- llm.ErrorEvent{Err: err}:
			case <-ctx.Done():
			}
		}
	}()

	return sr, nil
}

// providerName reports the wire format name for diagnostics.
func providerName(_ *llm.CompletionRequest) string { return "openai" }

// runStream scans an OpenAI SSE body. Frames are plain "data: {…}" lines; a
// blank line separates frames and "data: [DONE]" terminates the stream. Each
// chunk carries optional delta.content / delta.reasoning_content /
// delta.tool_calls and an optional finish_reason; the final chunk may carry
// usage. The terminal DoneEvent is synthesised after [DONE].
func runStream(ctx context.Context, r io.Reader, out chan<- llm.StreamEvent, model string) error {
	var (
		toolBuf        = map[int]*toolAccum{}
		finishReason   string
		usage          llm.Usage
		respModel      = model
		startEmitted   bool
		partialContent strings.Builder
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
			toolBuf:      toolBuf,
			startEmitted: &startEmitted,
			partial:      &partialContent,
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
			if payload == "[DONE]" {
				if err := flush(); err != nil {
					return err
				}
				return finishStream(out, &respModel, finishReason, usage, &partialContent, toolBuf, &startEmitted, ctx)
			}
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		case strings.HasPrefix(line, ":"):
			// SSE comment line; ignore.
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
		return llm.NewProviderError("openai", llm.ErrCodeInternal,
			fmt.Sprintf("sse scan: %s", err.Error()), 0, err)
	}
	return finishStream(out, &respModel, finishReason, usage, &partialContent, toolBuf, &startEmitted, ctx)
}

// finishStream flushes any in-flight tool accumulators as ToolCallEndEvent,
// guarantees StartEvent was emitted, and sends the terminal DoneEvent.
func finishStream(
	out chan<- llm.StreamEvent,
	respModel *string,
	finishReason string,
	usage llm.Usage,
	partial *strings.Builder,
	toolBuf map[int]*toolAccum,
	startEmitted *bool,
	ctx context.Context,
) error {
	for idx, ta := range toolBuf {
		if ta.id == "" && ta.name == "" && ta.arguments.Len() == 0 {
			continue
		}
		args := parseToolArgs(ta.arguments.String())
		out <- llm.ToolCallEndEvent{ID: ta.id, Name: ta.name, Arguments: args}
		delete(toolBuf, idx)
	}
	if !*startEmitted {
		select {
		case out <- llm.StartEvent{Partial: llm.AssistantMessage{Model: *respModel}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case out <- llm.DoneEvent{Response: llm.CompletionResponse{
		Model:        *respModel,
		Content:      partial.String(),
		FinishReason: mapFinishReason(finishReason),
		Usage:        usage,
	}}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// dispatchState bundles the mutable state threaded through SSE dispatch.
type dispatchState struct {
	model        string
	respModel    *string
	usage        *llm.Usage
	finishReason *string
	toolBuf      map[int]*toolAccum
	startEmitted *bool
	partial      *strings.Builder
	out          chan<- llm.StreamEvent
}

// chunk is the OpenAI chat.completion.chunk wire shape.
type chunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// dispatch routes a single OpenAI chunk to the unified channel.
func dispatch(raw string, st *dispatchState) error {
	var c chunk
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// Forward-compatible garbage: skip rather than abort the stream.
		return nil
	}
	if c.Model != "" {
		*st.respModel = c.Model
	}
	if c.Usage.PromptTokens > 0 || c.Usage.CompletionTokens > 0 {
		st.usage.PromptTokens = c.Usage.PromptTokens
		st.usage.CompletionTokens = c.Usage.CompletionTokens
		st.usage.TotalTokens = c.Usage.TotalTokens
	}
	if len(c.Choices) == 0 {
		return nil
	}

	ch := c.Choices[0]
	if !*st.startEmitted && (ch.Delta.Role == "assistant" || ch.Delta.Content != "" ||
		ch.Delta.ReasoningContent != "" || len(ch.Delta.ToolCalls) > 0) {
		*st.startEmitted = true
		select {
		case st.out <- llm.StartEvent{Partial: llm.AssistantMessage{Model: *st.respModel}}:
		default:
		}
	}

	if ch.Delta.ReasoningContent != "" {
		st.out <- llm.ThinkingDeltaEvent{Delta: ch.Delta.ReasoningContent}
	}
	if ch.Delta.Content != "" {
		st.partial.WriteString(ch.Delta.Content)
		st.out <- llm.TextDeltaEvent{Delta: ch.Delta.Content}
	}
	for _, tc := range ch.Delta.ToolCalls {
		ta, ok := st.toolBuf[tc.Index]
		if !ok {
			ta = &toolAccum{}
			st.toolBuf[tc.Index] = ta
		}
		if tc.ID != "" {
			ta.id = tc.ID
		}
		if tc.Function.Name != "" {
			ta.name = tc.Function.Name
		}
		// Emit ToolCallStart exactly once, on the first delta that carries
		// both id and name before any arguments accumulate.
		if ta.id != "" && ta.name != "" && !ta.started {
			ta.started = true
			st.out <- llm.ToolCallStartEvent{ID: ta.id, Name: ta.name}
		}
		if tc.Function.Arguments != "" {
			ta.arguments.WriteString(tc.Function.Arguments)
			st.out <- llm.ToolCallDeltaEvent{ID: ta.id, Delta: tc.Function.Arguments}
		}
	}
	if ch.FinishReason != nil && *ch.FinishReason != "" {
		*st.finishReason = *ch.FinishReason
	}
	return nil
}
