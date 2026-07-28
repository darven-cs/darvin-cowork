package tool

import (
	"fmt"
	"sort"

	"darvin-cowork/backend/internal/agent/llm"
)

// validateArgs checks that args satisfies schema. Supports the minimum JSON
// Schema subset the executor cares about: type=object with property types
// in {string,number,integer,boolean,array,object} and a required list.
//
// This is intentionally tiny — no $ref, no allOf/anyOf, no enum/default/
// format. Future specs can swap in a fuller validator (e.g. gojsonschema)
// without changing the call sites.
func validateArgs(name string, args map[string]any, schema llm.ParameterSchema) error {
	if schema.Type != "" && schema.Type != "object" {
		return fmt.Errorf("tool %q: only object schemas supported, got %q", name, schema.Type)
	}
	for _, k := range schema.Required {
		v, ok := args[k]
		if !ok || v == nil {
			return fmt.Errorf("tool %q: missing required argument %q", name, k)
		}
	}
	for propName, prop := range schema.Properties {
		v, ok := args[propName]
		if !ok || v == nil {
			continue
		}
		if err := checkType(name, propName, v, prop.Type); err != nil {
			return err
		}
	}
	// surface unknown args (likely model mistake) — not fatal but helpful
	if extra := unknownArgs(args, schema.Properties); len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("tool %q: unknown arguments: %v", name, extra)
	}
	return nil
}

func checkType(toolName, propName string, v any, want string) error {
	switch want {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("tool %q: argument %q must be string, got %T", toolName, propName, v)
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("tool %q: argument %q must be number, got %T", toolName, propName, v)
		}
	case "integer":
		// JSON numbers decode to float64; tests and other Go callers may
		// pass int / int64 / int32 directly. Accept any numeric type.
		switch v.(type) {
		case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		default:
			return fmt.Errorf("tool %q: argument %q must be integer, got %T", toolName, propName, v)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("tool %q: argument %q must be boolean, got %T", toolName, propName, v)
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return fmt.Errorf("tool %q: argument %q must be array, got %T", toolName, propName, v)
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("tool %q: argument %q must be object, got %T", toolName, propName, v)
		}
	case "":
		// no type declared — accept anything
	default:
		return fmt.Errorf("tool %q: argument %q has unsupported schema type %q", toolName, propName, want)
	}
	return nil
}

func unknownArgs(args map[string]any, props map[string]llm.ParameterProperty) []string {
	var extra []string
	for k := range args {
		if _, ok := props[k]; !ok {
			extra = append(extra, k)
		}
	}
	return extra
}
