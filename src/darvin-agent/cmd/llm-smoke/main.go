// llm-smoke is a manual end-to-end test driver for the darvin-agent LLM
// layer. It exercises both the non-streaming (Complete) and streaming
// (Stream) code paths against the real Anthropic Messages API.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	# optional overrides:
//	export ANTHROPIC_MODEL=claude-sonnet-4-5
//	export ANTHROPIC_BASE_URL=                # default: https://api.anthropic.com
//
//	go run ./cmd/llm-smoke                  # both modes
//	go run ./cmd/llm-smoke -mode complete   # only Complete
//	go run ./cmd/llm-smoke -mode stream     # only Stream
//
// The program is intentionally separate from cmd/app so it can be run
// without touching the production wiring.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"darvin-cowork/backend/internal/agent/llm"
	// Triggers anthropic.init() registration.
	_ "darvin-cowork/backend/internal/agent/llm/anthropic"
	"darvin-cowork/backend/internal/logger"
)

const (
	defaultModel     = "claude-sonnet-4-5"
	defaultBaseURL   = ""
	promptComplete   = "Reply with a single sentence describing what darvin-cowork is."
	promptStreamText = "Count from 1 to 5, one number per line."
)

func main() {
	mode := flag.String("mode", "both", "complete | stream | both")
	flag.Parse()

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY not set")
		os.Exit(2)
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = defaultModel
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	if err := logger.Init(&logger.Config{
		Level:    "info",
		Encoding: "console",
		Output:   "stdout",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "logger init: %v\n", err)
		os.Exit(1)
	}
	log := logger.Get().Sugar()
	defer log.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	provider, err := llm.NewProvider(ctx, "anthropic", llm.ProviderConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
		Logger:  log,
	})
	if err != nil {
		log.Fatalw("provider construction failed", "error", err)
	}
	log.Infow("provider ready", "name", provider.Name(), "model", model)

	exitCode := 0

	if *mode == "complete" || *mode == "both" {
		fmt.Println("================ Complete ================")
		if err := runComplete(ctx, provider, model, log); err != nil {
			fmt.Fprintf(os.Stderr, "complete failed: %v\n", err)
			exitCode = 1
		}
	}

	if *mode == "stream" || *mode == "both" {
		fmt.Println("\n================ Stream ================")
		if err := runStream(ctx, provider, model, log); err != nil {
			fmt.Fprintf(os.Stderr, "stream failed: %v\n", err)
			exitCode = 1
		}
	}

	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// runComplete exercises the non-streaming code path. It prints the
// returned content and tool calls as pretty JSON.
func runComplete(ctx context.Context, p llm.ModelProvider, model string, log llm.Logger) error {
	req := &llm.CompletionRequest{
		Model:     model,
		System:    "You are concise.",
		MaxTokens: 256,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: promptComplete},
		},
	}

	start := time.Now()
	resp, err := p.Complete(ctx, req)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}

	fmt.Printf("elapsed: %s\n", elapsed)
	fmt.Printf("model: %s\n", resp.Model)
	fmt.Printf("finish_reason: %s\n", resp.FinishReason)
	fmt.Printf("usage: %s\n", formatUsage(resp.Usage))
	fmt.Printf("content:\n%s\n", resp.Content)
	if len(resp.ToolCalls) > 0 {
		raw, _ := json.MarshalIndent(resp.ToolCalls, "", "  ")
		fmt.Printf("tool_calls:\n%s\n", raw)
	}
	return nil
}

// runStream exercises the streaming code path with a tool definition so
// the unified event sequence covers text deltas + tool calls.
func runStream(ctx context.Context, p llm.ModelProvider, model string, log llm.Logger) error {
	req := &llm.CompletionRequest{
		Model:     model,
		System:    "You are concise. When the user asks for weather, use the get_weather tool.",
		MaxTokens: 256,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: promptStreamText},
		},
		Tools: []llm.Tool{
			{
				Name:        "get_weather",
				Description: "Get the current weather for a location.",
				Parameters: llm.ParameterSchema{
					Type: "object",
					Properties: map[string]llm.ParameterProperty{
						"location": {Type: "string", Description: "City name"},
						"unit":     {Type: "string", Enum: []any{"celsius", "fahrenheit"}},
					},
					Required: []string{"location"},
				},
			},
		},
		ToolChoice: llm.ToolChoice{Type: "auto"},
	}

	start := time.Now()
	sr, err := p.Stream(ctx, req)
	if err != nil {
		return err
	}
	defer sr.Close()

	fmt.Print("events:\n")
	for ev := range sr.Events {
		printEvent(ev)
	}
	elapsed := time.Since(start)

	if err := sr.Err(); err != nil {
		return err
	}

	fmt.Printf("elapsed: %s\n", elapsed)
	return nil
}

// printEvent renders a single StreamEvent in a human-readable form.
func printEvent(ev llm.StreamEvent) {
	switch e := ev.(type) {
	case llm.StartEvent:
		fmt.Printf("  [start] model=%s\n", e.Partial.Model)
	case llm.TextDeltaEvent:
		fmt.Printf("  [text] %q\n", e.Delta)
	case llm.ToolCallStartEvent:
		fmt.Printf("  [tool_start] id=%s name=%s\n", e.ID, e.Name)
	case llm.ToolCallDeltaEvent:
		fmt.Printf("  [tool_delta] id=%s delta=%q\n", e.ID, e.Delta)
	case llm.ToolCallEndEvent:
		raw, _ := json.Marshal(e.Arguments)
		fmt.Printf("  [tool_end] id=%s name=%s args=%s\n", e.ID, e.Name, raw)
	case llm.DoneEvent:
		fmt.Printf("  [done] model=%s finish=%s usage=%s content=%q\n",
			e.Response.Model, e.Response.FinishReason, formatUsage(e.Response.Usage), e.Response.Content)
		if len(e.Response.ToolCalls) > 0 {
			raw, _ := json.MarshalIndent(e.Response.ToolCalls, "", "    ")
			fmt.Printf("    tool_calls:\n%s\n", raw)
		}
	case llm.ErrorEvent:
		fmt.Printf("  [error] %v\n", e.Err)
	default:
		fmt.Printf("  [unknown] %T %+v\n", ev, ev)
	}
}

func formatUsage(u llm.Usage) string {
	return fmt.Sprintf("prompt=%d completion=%d total=%d",
		u.PromptTokens, u.CompletionTokens, u.TotalTokens)
}