// Validates canonicalized MCP tool parameter schemas with a strict JSON Schema compiler.

package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const toolSchemaResource = "urn:darvin:tool-schema"

// ValidateToolSchema validates a canonicalized tool parameter schema using a strict
// JSON Schema compiler.
//
// Rules:
//   - Root "type" must be exactly "object". Nil or non-object types are rejected.
//     (CanonicalizeSchema already fills in missing root types; non-object
//     declared types are preserved verbatim so this catches them.)
//   - The jsonschema compiler rejects malformed syntax anywhere in the tree
//     (e.g. items.type:"", unrecognized types, bad $refs) by failing compile.
//   - External $ref resolution is disabled (UseLoader(nil)) so schemas cannot
//     read files or hit the network.
func ValidateToolSchema(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc any
	if err := decoder.Decode(&doc); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	// Verify exactly one JSON value was decoded.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid JSON: multiple top-level values")
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return fmt.Errorf("root must be an object")
	}
	// Root type must be "object" — the Anthropic/OpenAI tool contract requires it.
	switch typ := obj["type"].(type) {
	case string:
		if typ != "object" {
			return fmt.Errorf("root type must be %q, got %q", "object", typ)
		}
	case nil:
		return fmt.Errorf("root schema must declare type %q", "object")
	default:
		return fmt.Errorf("root type must be %q, got %s", "object", schemaJSONString(typ))
	}

	// Compile the schema with a disabled loader to prevent file:// or http:// refs.
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(nil) // no filesystem / network access
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource(toolSchemaResource, doc); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	if _, err := compiler.Compile(toolSchemaResource); err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return nil
}
