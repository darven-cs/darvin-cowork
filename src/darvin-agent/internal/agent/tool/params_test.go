package tool

import (
	"strings"
	"testing"

	"darvin-cowork/backend/internal/agent/llm"
)

func TestValidateArgsRequired(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"path": {Type: "string"},
		},
		Required: []string{"path"},
	}
	if err := validateArgs("t", map[string]any{}, schema); err == nil {
		t.Error("expected missing-required error")
	}
	if err := validateArgs("t", map[string]any{"path": "x"}, schema); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateArgsTypeMismatch(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"n":  {Type: "integer"},
			"b":  {Type: "boolean"},
			"xs": {Type: "array"},
		},
	}
	cases := map[string]any{
		"n":  "not a number",
		"b":  1,
		"xs": "not an array",
	}
	err := validateArgs("t", cases, schema)
	if err == nil {
		t.Fatal("expected type-mismatch error")
	}
	if !strings.Contains(err.Error(), "n") {
		t.Errorf("err = %v, should mention 'n'", err)
	}
}

func TestValidateArgsUnknown(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"known": {Type: "string"},
		},
	}
	err := validateArgs("t", map[string]any{"known": "x", "extra": 1}, schema)
	if err == nil {
		t.Fatal("expected unknown-arg error")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("err = %v, should mention 'extra'", err)
	}
}
