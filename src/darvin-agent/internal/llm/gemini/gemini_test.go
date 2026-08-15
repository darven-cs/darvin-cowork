// Tests for the native Gemini provider: conversion, SSE parsing, and HTTP.

package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"darvin-cowork/backend/internal/llm"
)

func TestBuildRequest_SystemInstruction(t *testing.T) {
	req := &llm.CompletionRequest{
		Model:    "gemini-3.1-pro-preview",
		System:   "be terse",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}
	out, err := buildRequest(req, false)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	sys, ok := out["systemInstruction"].(map[string]any)
	if !ok || sys["parts"].([]map[string]any)[0]["text"] != "be terse" {
		t.Errorf("systemInstruction = %+v", out["systemInstruction"])
	}
	contents, _ := out["contents"].([]map[string]any)
	if len(contents) != 1 || contents[0]["role"] != "user" {
		t.Errorf("contents = %+v", contents)
	}
}

func TestConvertContents_ToolRoundTrip(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "search", Name: "search", Arguments: map[string]any{"q": "mcp"}},
		}},
		{Role: llm.RoleTool, ToolCallID: "search", Content: "ok"},
	}
	contents := convertContents(msgs)
	if len(contents) != 2 {
		t.Fatalf("len = %d, want 2", len(contents))
	}
	modelParts := contents[0]["parts"].([]map[string]any)
	fc, ok := modelParts[0]["functionCall"].(map[string]any)
	if !ok || fc["name"] != "search" {
		t.Errorf("functionCall = %+v", modelParts[0])
	}
	userParts := contents[1]["parts"].([]map[string]any)
	fr, ok := userParts[0]["functionResponse"].(map[string]any)
	if !ok || fr["name"] != "search" {
		t.Errorf("functionResponse = %+v", userParts[0])
	}
}

func TestParseResponse_ToolCall(t *testing.T) {
	body := `{
		"candidates": [{
			"content": {"role": "model", "parts": [
				{"text": "checking"},
				{"functionCall": {"name": "search", "args": {"q": "x"}}}
			]},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 3, "totalTokenCount": 8}
	}`
	resp, err := parseResponse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Content != "checking" {
		t.Errorf("content = %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "search" || resp.ToolCalls[0].Arguments["q"] != "x" {
		t.Errorf("tool_calls = %+v", resp.ToolCalls)
	}
	if resp.Usage.PromptTokens != 5 || resp.Usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func collectStream(t *testing.T, sse string) []llm.StreamEvent {
	t.Helper()
	out := make(chan llm.StreamEvent, 64)
	err := runStream(context.Background(), strings.NewReader(sse), out, "gemini-3.1-pro-preview")
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
	events := collectStream(t, `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":null,"index":0}]}

data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP","index":0}]}

data: {"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}
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
				t.Errorf("content = %q", e.Response.Content)
			}
			if e.Response.Usage.PromptTokens != 4 {
				t.Errorf("usage = %+v", e.Response.Usage)
			}
		}
	}
	if !done {
		t.Fatal("missing DoneEvent")
	}
	if text != "Hello world" {
		t.Errorf("text = %q", text)
	}
}

func TestStream_ToolCall(t *testing.T) {
	events := collectStream(t, `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"search","args":{"q":"mcp"}}}]},"finishReason":"STOP","index":0}]}
`)
	var start, end int
	for _, ev := range events {
		switch e := ev.(type) {
		case llm.ToolCallStartEvent:
			start++
			if e.Name != "search" {
				t.Errorf("start name = %q", e.Name)
			}
		case llm.ToolCallEndEvent:
			end++
			if e.Arguments["q"] != "mcp" {
				t.Errorf("args = %+v", e.Arguments)
			}
		}
	}
	if start != 1 || end != 1 {
		t.Errorf("start=%d end=%d, want 1/1", start, end)
	}
}

func TestProvider_Complete(t *testing.T) {
	var gotAuth string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":3}}`))
	}))
	defer srv.Close()

	p := New(llm.ProviderConfig{APIKey: "AIza-test", BaseURL: srv.URL})
	resp, err := p.Complete(context.Background(), &llm.CompletionRequest{
		Model:    "gemini-3-flash-preview",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q", resp.Content)
	}
	if gotAuth != "AIza-test" {
		t.Errorf("x-goog-api-key = %q", gotAuth)
	}
	if gotPath != "/models/gemini-3-flash-preview:generateContent" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestProvider_StreamEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer srv.Close()

	p := New(llm.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	stream, err := p.Stream(context.Background(), &llm.CompletionRequest{
		Model:    "gemini-3-flash-preview",
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
	if text.String() != "ok" {
		t.Errorf("text = %q", text.String())
	}
	if !strings.Contains(gotPath, ":streamGenerateContent") {
		t.Errorf("path = %q, want streamGenerateContent", gotPath)
	}
}

func TestInit_RegistersModels(t *testing.T) {
	if _, ok := llm.DefaultModelRegistry.Get("gemini-3.1-pro-preview"); !ok {
		t.Error("gemini-3.1-pro-preview not registered")
	}
	m, ok := llm.DefaultModelRegistry.Get("gemini-3-flash-preview")
	if !ok {
		t.Fatal("gemini-3-flash-preview not registered")
	}
	if m.APIVersion != llm.APIGeminiGenerativeAI {
		t.Errorf("APIVersion = %q, want google-generative-ai", m.APIVersion)
	}
}
