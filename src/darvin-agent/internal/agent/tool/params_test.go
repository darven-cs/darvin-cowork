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

func TestValidateArgsUnknownArgsHardReject(t *testing.T) {
	// AdditionalProperties=false is the strict mode; undeclared fields must
	// be rejected even when the schema declares it explicitly.
	schema := llm.ParameterSchema{
		Type:                 "object",
		Properties:           map[string]llm.ParameterProperty{"known": {Type: "string"}},
		AdditionalProperties: ptrBool(false),
	}
	if err := validateArgs("t", map[string]any{"known": "x", "bogus": true}, schema); err == nil {
		t.Error("expected hard reject for undeclared arg with AdditionalProperties=false")
	}
}

func TestValidateArgsEnum(t *testing.T) {
	schema := llm.ParameterSchema{
		Type:       "object",
		Properties: map[string]llm.ParameterProperty{"command": {Type: "string", Enum: []string{"ls", "cat"}}},
	}
	if err := validateArgs("shell", map[string]any{"command": "ls"}, schema); err != nil {
		t.Errorf("valid enum value rejected: %v", err)
	}
	err := validateArgs("shell", map[string]any{"command": "rm -rf /"}, schema)
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Errorf("out-of-enum value err = %v, want enum rejection", err)
	}
}

func TestValidateArgsRange(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"limit": {Type: "integer", Minimum: ptrFloat64(0), Maximum: ptrFloat64(100)},
		},
	}
	if err := validateArgs("read_file", map[string]any{"limit": float64(50)}, schema); err != nil {
		t.Errorf("in-range value rejected: %v", err)
	}
	if err := validateArgs("read_file", map[string]any{"limit": float64(101)}, schema); err == nil {
		t.Error("out-of-range value should be rejected")
	}
	if err := validateArgs("read_file", map[string]any{"limit": float64(-1)}, schema); err == nil {
		t.Error("negative value should be rejected")
	}
}

func TestValidateArgsMaxLength(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"content": {Type: "string", MaxLength: ptrInt(5)},
		},
	}
	if err := validateArgs("write_file", map[string]any{"content": "abc"}, schema); err != nil {
		t.Errorf("short content rejected: %v", err)
	}
	if err := validateArgs("write_file", map[string]any{"content": "abcdef"}, schema); err == nil {
		t.Error("over-length content should be rejected")
	}
}

func TestValidateArgsPattern(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"name": {Type: "string", Pattern: "^[a-z0-9_-]+$"},
		},
	}
	if err := validateArgs("t", map[string]any{"name": "good_name-1"}, schema); err != nil {
		t.Errorf("valid pattern rejected: %v", err)
	}
	if err := validateArgs("t", map[string]any{"name": "bad name!"}, schema); err == nil {
		t.Error("pattern mismatch should be rejected")
	}
}

func TestValidateArgsItems(t *testing.T) {
	schema := llm.ParameterSchema{
		Type: "object",
		Properties: map[string]llm.ParameterProperty{
			"args": {Type: "array", Items: &llm.ParameterProperty{Type: "string"}},
		},
	}
	if err := validateArgs("shell", map[string]any{"args": []any{"-la"}}, schema); err != nil {
		t.Errorf("valid array rejected: %v", err)
	}
	err := validateArgs("shell", map[string]any{"args": []any{123}}, schema)
	if err == nil || !strings.Contains(err.Error(), "args[0]") {
		t.Errorf("array element type mismatch err = %v, want args[0] mention", err)
	}
}
