package tool

import (
	"fmt"
	"regexp"
	"sort"

	"darvin-cowork/backend/internal/agents/llm"
)

// validateArgs checks that args satisfies schema. Supports type=object with
// property types in {string,number,integer,boolean,array,object}, a required
// list, and per-property enum / numeric range / string length / pattern /
// array items constraints. Unknown (undeclared) args are a hard rejection —
// a model passing a misspelled field gets an error, not a silent ignore.
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
		if err := checkProperty(name, propName, v, prop); err != nil {
			return err
		}
	}
	// surface unknown args (likely model mistake) — fatal, kept for compat
	if extra := unknownArgs(args, schema.Properties); len(extra) > 0 {
		sort.Strings(extra)
		return fmt.Errorf("tool %q: unknown arguments: %v", name, extra)
	}
	return nil
}

// checkProperty validates one property value against its schema constraints.
func checkProperty(toolName, propName string, v any, prop llm.ParameterProperty) error {
	if err := checkType(toolName, propName, v, prop.Type); err != nil {
		return err
	}
	if len(prop.Enum) > 0 {
		matched := false
		for _, e := range prop.Enum {
			if e == v {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("tool %q: argument %q must be one of %v, got %v", toolName, propName, prop.Enum, v)
		}
	}
	if prop.Minimum != nil || prop.Maximum != nil {
		if num, ok := toFloat(v); ok {
			if prop.Minimum != nil && num < *prop.Minimum {
				return fmt.Errorf("tool %q: argument %q must be >= %v, got %v", toolName, propName, *prop.Minimum, v)
			}
			if prop.Maximum != nil && num > *prop.Maximum {
				return fmt.Errorf("tool %q: argument %q must be <= %v, got %v", toolName, propName, *prop.Maximum, v)
			}
		}
	}
	if prop.MinLength != nil || prop.MaxLength != nil {
		if s, ok := v.(string); ok {
			if prop.MinLength != nil && len(s) < *prop.MinLength {
				return fmt.Errorf("tool %q: argument %q must be at least %d characters, got %d", toolName, propName, *prop.MinLength, len(s))
			}
			if prop.MaxLength != nil && len(s) > *prop.MaxLength {
				return fmt.Errorf("tool %q: argument %q must be at most %d characters, got %d", toolName, propName, *prop.MaxLength, len(s))
			}
		}
	}
	if prop.Pattern != "" {
		if s, ok := v.(string); ok {
			re, err := regexp.Compile(prop.Pattern)
			if err != nil {
				return fmt.Errorf("tool %q: argument %q has invalid pattern %q", toolName, propName, prop.Pattern)
			}
			if !re.MatchString(s) {
				return fmt.Errorf("tool %q: argument %q does not match pattern %q", toolName, propName, prop.Pattern)
			}
		}
	}
	if prop.Items != nil {
		if xs, ok := v.([]any); ok {
			for i, x := range xs {
				if err := checkProperty(toolName, fmt.Sprintf("%s[%d]", propName, i), x, *prop.Items); err != nil {
					return err
				}
			}
		}
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

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

func ptrFloat64(f float64) *float64 { return &f }

func ptrInt(i int) *int { return &i }

func ptrBool(b bool) *bool { return &b }
