package jsonschema

import (
	"encoding/json"
	"sort"
)

// CanonicalizeSchema recursively normalizes an MCP tool's JSON Schema so that:
//
//   - Empty / nil raw bytes → strict {type:"object"} placeholder
//   - Missing root "type" → added as "object" (MCP tools routinely omit it)
//   - Missing "properties" on type:"object" → added as {}
//   - Non-array "required" (OpenAPI-style boolean metadata) → dropped
//   - "required" / array keys → sorted for stable fingerprints
//
// It intentionally does NOT rewrite or reject invalid "type" values (e.g. "type":""
// or "type":["object","null"]). Those are caught by ValidateToolSchema downstream
// so callers always see the same logical schema.
func CanonicalizeSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{"properties":{},"type":"object"}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	if v == nil {
		return json.RawMessage(`{"properties":{},"type":"object"}`)
	}
	canon := canonicalizeSchemaValue(v)
	ensureRootObjectProperties(canon)
	b, err := json.Marshal(canon)
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

// ensureRootObjectProperties fills in missing fields on the top-level object
// schema. Nested schemas are processed recursively by canonicalizeSchemaObject.
func ensureRootObjectProperties(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	if _, ok := m["type"]; !ok {
		// MCP servers routinely omit the root type; tool arguments are always
		// objects, so the omission can only mean an object schema. Make it explicit.
		m["type"] = "object"
	}
	if m["type"] != "object" {
		return
	}
	if _, ok := m["properties"]; !ok {
		m["properties"] = map[string]any{}
	}
}

func canonicalizeSchemaValue(v any) any { return canonicalizeSchemaObject(v) }

func canonicalizeSchemaObject(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, inner := range val {
			switch k {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
				val[k] = canonicalizeNamedSchemas(inner)
			case "dependentRequired":
				val[k] = canonicalizeDependentRequired(inner)
			default:
				val[k] = canonicalizeSchemaObject(inner)
			}
		}
		if req, ok := val["required"]; ok {
			if arr, ok := req.([]any); ok {
				sortSchemaArray(arr)
			} else {
				// OpenAPI-style boolean metadata ({"required": true}) is invalid JSON Schema.
				// Dropping keeps the whole tool list from being rejected with HTTP 400.
				delete(val, "required")
			}
		}
		if dr, ok := val["dependentRequired"]; ok && !isJSONObject(dr) {
			delete(val, "dependentRequired")
		}
		return val
	case []any:
		for i, elem := range val {
			val[i] = canonicalizeSchemaObject(elem)
		}
		return val
	default:
		return v
	}
}

func canonicalizeNamedSchemas(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return canonicalizeSchemaObject(v)
	}
	for name, schema := range m {
		m[name] = canonicalizeSchemaObject(schema)
	}
	return m
}

func canonicalizeDependentRequired(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for key, inner := range m {
		if arr, ok := inner.([]any); ok {
			sortSchemaArray(arr)
		} else {
			delete(m, key)
		}
	}
	return m
}

func isJSONObject(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

func sortSchemaArray(arr []any) {
	sort.SliceStable(arr, func(i, j int) bool {
		return schemaJSONString(arr[i]) < schemaJSONString(arr[j])
	})
}

func schemaJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
