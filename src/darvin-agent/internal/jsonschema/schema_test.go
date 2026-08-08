package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalizeSchema_EmptyAndNil(t *testing.T) {
	for _, raw := range []json.RawMessage{
		nil,
		json.RawMessage(`null`),
		json.RawMessage(`{"type":"object"}`),
	} {
		got := string(CanonicalizeSchema(raw))
		want := `{"properties":{},"type":"object"}`
		if got != want {
			t.Fatalf("CanonicalizeSchema(%s) = %s, want %s", string(raw), got, want)
		}
	}
}

func TestCanonicalizeSchema_FillsMissingRootType(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{}`, `{"properties":{},"type":"object"}`},
		{`{"properties":{"q":{"type":"string"}}}`, `{"properties":{"q":{"type":"string"}},"type":"object"}`},
		// Explicit non-object root types are preserved verbatim — validation
		// quarantines them instead of silently rewriting declared semantics.
		{`{"type":"string"}`, `{"type":"string"}`},
		{`{"type":["object","null"]}`, `{"type":["object","null"]}`},
	} {
		if got := string(CanonicalizeSchema(json.RawMessage(tc.raw))); got != tc.want {
			t.Fatalf("CanonicalizeSchema(%s) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

func TestCanonicalizeSchema_DropsNonArrayRequired(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","required":true},
			"nested":{"type":"object","required":false,"properties":{"x":{"type":"string"}}}
		},
		"required":["query","nested"]
	}`)
	got := string(CanonicalizeSchema(raw))
	want := `{"properties":{"nested":{"properties":{"x":{"type":"string"}},"type":"object"},"query":{"type":"string"}},"required":["nested","query"],"type":"object"}`
	if got != want {
		t.Fatalf("CanonicalizeSchema() = %s, want %s", got, want)
	}
}

func TestValidateToolSchema_AcceptsValidObject(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	if err := ValidateToolSchema(raw); err != nil {
		t.Fatalf("ValidateToolSchema: %v", err)
	}
}

func TestValidateToolSchema_RejectsMissingRootType(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"missing":  json.RawMessage(`{}`),
		"string":   json.RawMessage(`{"type":"string"}`),
		"nullable": json.RawMessage(`{"type":["object","null"]}`),
		"empty":    json.RawMessage(`{"type":""}`),
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateToolSchema(raw)
			if err == nil {
				t.Fatalf("ValidateToolSchema(%s) returned nil", string(raw))
			}
			if !strings.Contains(err.Error(), "object") {
				t.Fatalf("error does not name the object requirement: %v", err)
			}
		})
	}
}

func TestValidateToolSchema_RejectsMalformedNestedArrayItems(t *testing.T) {
	// GitHub MCP's create_pull_request_review emits comments.items.type = "".
	// The jsonschema compiler rejects this nested bad type.
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{
			"comments":{
				"type":"array",
				"items":{"type":""}
			}
		}
	}`)
	err := ValidateToolSchema(raw)
	if err == nil {
		t.Fatal("malformed nested items.type was accepted")
	}
}

func TestValidateToolSchema_RejectsArrayRoot(t *testing.T) {
	raw := json.RawMessage(`[]`)
	if err := ValidateToolSchema(raw); err == nil {
		t.Fatal("array root was accepted")
	}
}
