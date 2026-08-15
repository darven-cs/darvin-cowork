// Parses Anthropic SSE stream frames into the unified StreamEvent contract.

package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"darvin-cowork/backend/internal/llm"
)

// streamEventName is the value of an Anthropic SSE "event:" line.
// Anthropic emits a single event name per chunk followed by one or more
// "data:" lines (we only care about the first one).
type streamEventName string

const (
	evMessageStart      streamEventName = "message_start"
	evContentBlockStart streamEventName = "content_block_start"
	evContentBlockDelta streamEventName = "content_block_delta"
	evContentBlockStop  streamEventName = "content_block_stop"
	evMessageDelta      streamEventName = "message_delta"
	evMessageStop       streamEventName = "message_stop"
	evPing              streamEventName = "ping"
	evError             streamEventName = "error"
)

// toolAccum tracks the per-index tool_use state during streaming so we can
// emit a single ToolCallStart / multiple ToolCallDelta / single ToolCallEnd
// triplet per invocation, matching the unified StreamEvent contract.
type toolAccum struct {
	id        string
	name      string
	arguments strings.Builder
}

// openStream opens a streaming call and returns the unified StreamingResponse.
//
// The body returned by the underlying HTTP client is consumed by a goroutine
// that translates each Anthropic SSE event into one of our StreamEvent
// shapes and pushes them onto the events channel. The goroutine owns the
// body and closes the channel on exit.
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
	body, err := hc.DoStream(ctx, "anthropic", url, headers, payload)
	if err != nil {
		return nil, err
	}

	events := make(chan llm.StreamEvent, 16)
	sr := llm.NewStreamingResponse(events, body)

	go func() {
		defer close(events)
		defer body.Close()

		if err := runStream(ctx, body, events, req.Model); err != nil {
			// Surface as ErrorEvent; do not propagate via SetErr here —
			// callers read Err() after the channel drains.
			sr.SetErr(err)
			select {
			case events <- llm.ErrorEvent{Err: err}:
			case <-ctx.Done():
			}
		}
	}()

	return sr, nil
}

// runStream scans an Anthropic SSE body, dispatching each frame to the
// unified event channel. It returns the terminal error if any.
//
// State machine (per Anthropic SSE spec):
//
//	message_start       → StartEvent + initial model / usage snapshot
//	content_block_start → ToolCallStartEvent (tool_use) | noop (text)
//	content_block_delta → TextDeltaEvent | ToolCallDeltaEvent
//	content_block_stop  → ToolCallEndEvent (final JSON parsed)
//	message_delta       → captures final stop_reason / usage
//	message_stop        → DoneEvent (final)
//	error               → ErrorEvent
//	ping                → ignored
func runStream(ctx context.Context, r io.Reader, out chan<- llm.StreamEvent, model string) error {
	// Pre-allocate the index → tool buffer.
	var (
		mu             sync.Mutex
		toolBuf        = map[int]*toolAccum{}
		textIndex      = -1
		stopReason     string
		usage          llm.Usage
		respModel      = model
		startEmitted   bool
		partialContent strings.Builder
		collectedCalls []llm.ToolCall
		splitter       = llm.NewThinkingSplitter()
	)

	scanner := bufio.NewScanner(r)
	// Anthropic frames are small but a single text_delta can exceed the
	// default 64KiB scanner buffer; bump to 1 MiB to be safe.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		eventName streamEventName
		dataBuf   strings.Builder
	)

	flush := func() error {
		if dataBuf.Len() == 0 {
			eventName = ""
			return nil
		}
		defer func() {
			eventName = ""
			dataBuf.Reset()
		}()
		raw := dataBuf.String()
		err := dispatch(eventName, raw, &dispatchState{
			model:        model,
			respModel:    &respModel,
			usage:        &usage,
			stopReason:   &stopReason,
			toolBuf:      toolBuf,
			textIndex:    &textIndex,
			startEmitted: &startEmitted,
			partial:      &partialContent,
			collected:    &collectedCalls,
			splitter:     splitter,
			out:          out,
		})
		return err
	}

	for scanner.Scan() {
		// Cooperative cancellation check between frames.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		switch {
		case line == "":
			// SSE frame delimiter: dispatch whatever we've accumulated.
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "event: "):
			eventName = streamEventName(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			payload := strings.TrimPrefix(line, "data: ")
			if dataBuf.Len() > 0 {
				// Anthropic uses a single data: line per frame; concatenate
				// defensively for non-conforming proxies.
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		case strings.HasPrefix(line, ":"): //nolint:staticcheck // case guard consumes the value
			// SSE comment / ping-style line; ignore.
			continue
		default:
			// Unknown prefix; ignore.
			continue
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		// Distinguish context cancellation from transport EOFs.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ctx.Err()
		}
		return llm.NewProviderError("anthropic", llm.ErrCodeInternal,
			fmt.Sprintf("sse scan: %s", err.Error()), 0, err)
	}

	// Synthesise DoneEvent at end-of-stream when we have not seen message_stop
	// (some proxies truncate the trailing event).
	mu.Lock()
	defer mu.Unlock()

	// Flush any in-flight tool accumulators that did not get content_block_stop.
	for idx, ta := range toolBuf {
		if ta.arguments.Len() == 0 {
			continue
		}
		collectedCalls = append(collectedCalls, llm.ToolCall{
			ID:        ta.id,
			Name:      ta.name,
			Arguments: parseToolArgs(ta.arguments.String()),
		})
		delete(toolBuf, idx)
	}

	// Ensure StartEvent was emitted even for edge cases where message_start
	// was missing (theoretical; defensive).
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
		ToolCalls:    collectedCalls,
		FinishReason: mapStopReason(stopReason),
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
	stopReason   *string
	toolBuf      map[int]*toolAccum
	textIndex    *int
	startEmitted *bool
	partial      *strings.Builder
	collected    *[]llm.ToolCall
	splitter     *llm.ThinkingSplitter
	out          chan<- llm.StreamEvent
}

// dispatch routes a single Anthropic SSE frame to the unified channel.
func dispatch(name streamEventName, raw string, st *dispatchState) error {
	switch name {
	case evPing:
		return nil
	case evMessageStart:
		var msg struct {
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			return errInvalidRequest("message_start parse: " + err.Error())
		}
		*st.respModel = msg.Message.Model
		st.usage.PromptTokens = msg.Message.Usage.InputTokens
		st.usage.CompletionTokens = msg.Message.Usage.OutputTokens
		st.usage.TotalTokens = msg.Message.Usage.InputTokens + msg.Message.Usage.OutputTokens
		if !*st.startEmitted {
			*st.startEmitted = true
			select {
			case st.out <- llm.StartEvent{Partial: llm.AssistantMessage{Model: msg.Message.Model}}:
			default:
				// Channel is buffered; should not block here.
			}
		}
		return nil
	case evContentBlockStart:
		var blk struct {
			Index        int    `json:"index"`
			BlockType    string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(raw), &blk); err != nil {
			return errInvalidRequest("content_block_start parse: " + err.Error())
		}
		switch blk.ContentBlock.Type {
		case "text":
			*st.textIndex = blk.Index
		case "tool_use":
			ta := &toolAccum{id: blk.ContentBlock.ID, name: blk.ContentBlock.Name}
			st.toolBuf[blk.Index] = ta
			st.out <- llm.ToolCallStartEvent{ID: ta.id, Name: ta.name}
		}
		return nil
	case evContentBlockDelta:
		var d struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			return errInvalidRequest("content_block_delta parse: " + err.Error())
		}
		switch d.Delta.Type {
		case "text_delta":
			for _, ev := range st.splitter.Feed(d.Delta.Text) {
				if te, ok := ev.(llm.TextDeltaEvent); ok {
					st.partial.WriteString(te.Delta)
				}
				st.out <- ev
			}
		case "thinking_delta":
			// Anthropic extended-thinking emits incremental chunks via the
			// "thinking" field; surface each as a ThinkingDeltaEvent so the
			// executor can republish it under EventCommon.MessageID.
			st.out <- llm.ThinkingDeltaEvent{Delta: d.Delta.Thinking}
		case "input_json_delta":
			if ta, ok := st.toolBuf[d.Index]; ok {
				ta.arguments.WriteString(d.Delta.PartialJSON)
				st.out <- llm.ToolCallDeltaEvent{ID: ta.id, Delta: d.Delta.PartialJSON}
			}
		}
		return nil
	case evContentBlockStop:
		var blk struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(raw), &blk); err != nil {
			return errInvalidRequest("content_block_stop parse: " + err.Error())
		}
		if ta, ok := st.toolBuf[blk.Index]; ok {
			args := parseToolArgs(ta.arguments.String())
			tc := llm.ToolCall{ID: ta.id, Name: ta.name, Arguments: args}
			*st.collected = append(*st.collected, tc)
			delete(st.toolBuf, blk.Index)
			st.out <- llm.ToolCallEndEvent{ID: ta.id, Name: ta.name, Arguments: args}
		}
		return nil
	case evMessageDelta:
		var d struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			// Tolerate malformed message_delta (e.g. from proxies).
			return nil
		}
		if d.Delta.StopReason != "" {
			*st.stopReason = d.Delta.StopReason
		}
		if d.Usage.OutputTokens > 0 {
			st.usage.CompletionTokens = d.Usage.OutputTokens
			st.usage.TotalTokens = st.usage.PromptTokens + d.Usage.OutputTokens
		}
		return nil
	case evMessageStop:
		// The terminal event; DoneEvent is emitted by runStream after
		// draining pending tool buffers so all data lands in CompletionResponse.
		return nil
	case evError:
		var e struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(raw), &e)
		return llm.NewProviderError("anthropic",
			anthropicErrorCode(e.Error.Type),
			e.Error.Message,
			0, nil)
	default:
		// Unknown / forward-compatible event: ignore.
		return nil
	}
}

// anthropicErrorCode maps Anthropic's error.type to the unified code.
func anthropicErrorCode(t string) string {
	switch t {
	case "authentication_error":
		return llm.ErrCodeAuth
	case "rate_limit_error":
		return llm.ErrCodeRateLimit
	case "invalid_request_error":
		return llm.ErrCodeInvalidRequest
	default:
		return llm.ErrCodeInternal
	}
}

// parseToolArgs converts the accumulated partial JSON into a map. Empty
// input yields an empty (non-nil) map so callers can range over it.
func parseToolArgs(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		// Fall back to a synthetic {"raw": <string>} so the model still
		// sees what it produced; surfacing the parse error here would
		// abort an otherwise usable stream.
		return map[string]any{"_raw": s, "_error": err.Error()}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}
