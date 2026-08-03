package ctxengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"darvin-cowork/backend/internal/llm"
)

// Compact runs the LLM-based compaction pipeline. It never mutates
// p.Messages — the caller adopts RetainedMessages only on Success.
func (a *DefaultAssembler) Compact(ctx context.Context, p CompactParams) CompactResult {
	if err := ctx.Err(); err != nil {
		return CompactResult{
			Success:          false,
			RetainedMessages: p.Messages,
		}
	}

	a.mu.RLock()
	cfg := a.cfg
	summarizer := a.summarizer
	var modelName string
	if a.deps != nil {
		modelName = a.deps.ModelName()
	}
	a.mu.RUnlock()

	snap := p.Checkpoint
	if snap == nil {
		snap = &CheckPoint{
			ID:         newCheckpointID(),
			CapturedAt: time.Now(),
			Snapshot:   cloneMessages(p.Messages),
		}
	} else {
		snap.Snapshot = cloneMessages(p.Messages)
	}

	tokensBefore := p.LastUsage.PromptTokens
	if tokensBefore <= 0 {
		tokensBefore = estimateMessages(p.Messages)
	}

	if !p.Force && tokensBefore <= p.Budget {
		return CompactResult{
			Success:          true,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
		}
	}

	if summarizer == nil {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
		}
	}

	tail := cfg.CompactTailKeep
	if tail <= 0 {
		tail = 6
	}
	if tail >= len(p.Messages) {
		tail = len(p.Messages) - 1
		if tail < 0 {
			tail = 0
		}
	}
	span := p.Messages[:len(p.Messages)-tail]
	if len(span) == 0 {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
		}
	}

	summaryText, err := summarizer.Summarize(ctx, SummarizeRequest{
		Model:     modelName,
		Messages:  span,
		Hint:      "conversational summary; preserve tool input/output facts and decisions",
		MaxTokens: cfg.SummarizeMaxTokens,
	})
	if err != nil {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensBefore,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
		}
	}

	summaryMsg := llm.Message{
		Role: llm.RoleAssistant,
		Content: "[Conversation Summary]\n" + summaryText +
			fmt.Sprintf("\n\n(Compacted at %s; original %d messages → tail %d messages)",
				time.Now().Format(time.RFC3339), len(span), tail),
	}
	newMessages := append([]llm.Message{summaryMsg}, p.Messages[len(p.Messages)-tail:]...)
	tokensAfter := estimateMessages(newMessages)

	retries := 0
	for tokensAfter > p.Budget && retries < cfg.CompactMaxRetries {
		half := len(span) / 2
		if half == 0 {
			break
		}
		span = p.Messages[:half]
		newSpan, err := summarizer.Summarize(ctx, SummarizeRequest{
			Model:     modelName,
			Messages:  span,
			Hint:      "compress further",
			MaxTokens: cfg.SummarizeMaxTokens,
		})
		if err != nil {
			break
		}
		summaryMsg.Content = "[Conversation Summary]\n" + newSpan +
			fmt.Sprintf("\n\n(Recompacted %d times)", retries+1)
		tailStart := half
		newMessages = append([]llm.Message{summaryMsg}, p.Messages[tailStart:]...)
		tokensAfter = estimateMessages(newMessages)
		retries++
	}

	if tokensAfter > p.Budget {
		return CompactResult{
			Success:          false,
			TokensBefore:     tokensBefore,
			TokensAfter:      tokensAfter,
			RetainedMessages: p.Messages,
			Checkpoint:       snap,
		}
	}

	return CompactResult{
		Success:          true,
		TokensBefore:     tokensBefore,
		TokensAfter:      tokensAfter,
		RetainedMessages: newMessages,
		Summary:          summaryMsg.Content,
		Checkpoint:       snap,
	}
}

// DefaultSummarizer is the default Summarizer: it wraps
// llm.ModelProvider.Complete with a summarisation-specific system prompt.
// It deliberately does not reuse Agent.Session or Agent.EventBus so the
// summary call does not pollute the agent's own conversation state.
type DefaultSummarizer struct {
	provider llm.ModelProvider
}

// NewDefaultSummarizer constructs the default Summarizer. Provider may be
// nil — Summarize returns an error in that case.
func NewDefaultSummarizer(provider llm.ModelProvider) *DefaultSummarizer {
	return &DefaultSummarizer{provider: provider}
}

// Summarize sends req.Messages to the provider with a summariser system
// prompt and returns the assistant content.
func (s *DefaultSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	if s.provider == nil {
		return "", fmt.Errorf("ctxengine: summarizer provider is nil")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 800
	}
	system := "You are a conversation summarizer. Output a concise summary preserving tool inputs/outputs, decisions, and context that future turns might need. " + req.Hint
	resp, err := s.provider.Complete(ctx, &llm.CompletionRequest{
		Model:     req.Model,
		Messages:  req.Messages,
		System:    system,
		MaxTokens: maxTokens,
		Stream:    false,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// estimateMessages sums EstimateMessageTokens over a slice.
func estimateMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += EstimateMessageTokens(m)
	}
	return n
}

func newCheckpointID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("cp-%d", time.Now().UnixNano())
	}
	return "cp-" + hex.EncodeToString(b[:])
}
