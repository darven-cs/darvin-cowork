// Tests for the OpenAI-compatible Provider: init registration, headers,
// and end-to-end Complete / Stream against an httptest server.

package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

func TestProvider_Name(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "sk-test"})
	if got := p.Name(); got != "openai" {
		t.Errorf("Name() = %q, want %q", got, "openai")
	}
}

func TestProvider_Headers_WithKey(t *testing.T) {
	p := New(llm.ProviderConfig{APIKey: "sk-test-abc"})
	h := p.headers()
	if h["Authorization"] != "Bearer sk-test-abc" {
		t.Errorf("Authorization = %q, want bearer", h["Authorization"])
	}
}

func TestProvider_Headers_NoKey(t *testing.T) {
	// Keyless gateways (local Ollama) must not emit an Authorization header.
	p := New(llm.ProviderConfig{APIKey: ""})
	if _, ok := p.headers()["Authorization"]; ok {
		t.Error("Authorization header present for keyless provider")
	}
}

func TestProvider_BaseURL_DefaultAndTrim(t *testing.T) {
	if got := New(llm.ProviderConfig{}).baseURL; got != "https://api.openai.com/v1" {
		t.Errorf("default baseURL = %q", got)
	}
	if got := New(llm.ProviderConfig{BaseURL: "https://proxy.example.com/v1/"}).baseURL; got != "https://proxy.example.com/v1" {
		t.Errorf("trimmed baseURL = %q", got)
	}
}

func TestInit_RegistersModels(t *testing.T) {
	openaiModels := llm.DefaultModelRegistry.ListByProvider("openai")
	if len(openaiModels) < 4 {
		t.Fatalf("expected ≥4 openai models, got %d", len(openaiModels))
	}
	if _, ok := llm.DefaultModelRegistry.Get("gpt-4o"); !ok {
		t.Error("gpt-4o not registered")
	}
	if _, ok := llm.DefaultModelRegistry.Get("gpt-5.4"); !ok {
		t.Error("gpt-5.4 not registered")
	}
	if _, ok := llm.DefaultModelRegistry.Get("gpt-5.5"); !ok {
		t.Error("gpt-5.5 not registered")
	}
}

func TestProvider_Complete(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-1",
			"model": "gpt-4o",
			"choices": [{"index": 0, "message": {"role": "assistant", "content": "hi back"}, "finish_reason": "stop"}],
			"usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}
		}`))
	}))
	defer srv.Close()

	p := New(llm.ProviderConfig{APIKey: "sk-x", BaseURL: srv.URL})
	resp, err := p.Complete(context.Background(), &llm.CompletionRequest{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Stream:   false,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi back" {
		t.Errorf("content = %q, want %q", resp.Content, "hi back")
	}
	if resp.FinishReason != llm.FinishReasonStop {
		t.Errorf("finish_reason = %v", resp.FinishReason)
	}
	if gotAuth != "Bearer sk-x" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	if gotBody["model"] != "gpt-4o" {
		t.Errorf("model = %v", gotBody["model"])
	}
}

func TestProvider_Stream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"a"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"b"},"finish_reason":"stop"}]}

data: [DONE]
`))
	}))
	defer srv.Close()

	p := New(llm.ProviderConfig{APIKey: "sk-x", BaseURL: srv.URL})
	stream, err := p.Stream(context.Background(), &llm.CompletionRequest{
		Model:    "gpt-4o",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text strings.Builder
	for ev := range stream.Events {
		if e, ok := ev.(llm.TextDeltaEvent); ok {
			text.WriteString(e.Delta)
		}
	}
	if text.String() != "ab" {
		t.Errorf("text = %q, want %q", text.String(), "ab")
	}
}
