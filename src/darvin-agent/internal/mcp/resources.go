// Resources and prompts support: the MCP resources/list + resources/read
// and prompts/list + prompts/get client methods, plus their wire types.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ResourceDescriptor is one entry in a resources/list result.
type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceContent is one entry in a resources/read result.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// PromptDescriptor is one entry in a prompts/list result.
type PromptDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument is a templated input a prompt accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage is one message in a rendered prompt.
type PromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// ListResources asks the server for the resources it exposes.
func (c *Client) ListResources(ctx context.Context) ([]ResourceDescriptor, error) {
	raw, err := c.Call(ctx, "resources/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Resources []ResourceDescriptor `json:"resources"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal resources/list result: %w", err)
	}
	return result.Resources, nil
}

// ReadResource fetches the content of one resource by URI.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	raw, err := c.Call(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, err
	}
	var result struct {
		Contents []ResourceContent `json:"contents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal resources/read result: %w", err)
	}
	return result.Contents, nil
}

// ListPrompts asks the server for the prompt templates it exposes.
func (c *Client) ListPrompts(ctx context.Context) ([]PromptDescriptor, error) {
	raw, err := c.Call(ctx, "prompts/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Prompts []PromptDescriptor `json:"prompts"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal prompts/list result: %w", err)
	}
	return result.Prompts, nil
}

// GetPrompt renders one prompt template with the given argument values.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]any) ([]PromptMessage, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	raw, err := c.Call(ctx, "prompts/get", params)
	if err != nil {
		return nil, err
	}
	var result struct {
		Messages []PromptMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp: unmarshal prompts/get result: %w", err)
	}
	return result.Messages, nil
}
