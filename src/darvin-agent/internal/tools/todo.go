// todo_write and complete_step built-in tools: stateless structured
// task tracking. The full checklist lives in the call arguments
// (conversation history) — the tools only validate shape and
// acknowledge counts; no host-side storage.

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// maxTodoItems caps a single todo_write list.
	maxTodoItems = 50
	// maxEvidenceItems caps a single complete_step evidence list.
	maxEvidenceItems = 5
)

// todoItem is one entry in a todo_write list. Level 0 is a milestone
// (PHASE), level 1 a sub-step under a milestone.
type todoItem struct {
	Content    string
	Status     string
	ActiveForm string
	Level      int
}

// stepEvidence is one sign-off evidence item for complete_step.
type stepEvidence struct {
	Kind        string
	Description string
}

// todoWriteTool records or replaces the current task checklist.
type todoWriteTool struct{}

func (todoWriteTool) Name() string { return "todo_write" }
func (todoWriteTool) Description() string {
	return "Record or update the structured task checklist for the current work. The tool is stateless: send the FULL list on every call (args are the state) and it replaces the previous one. level=0 marks a milestone (PHASE), level=1 a sub-step under it. At most one item may be in_progress."
}
func (todoWriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"todos": {
				"type": "array",
				"minItems": 0,
				"maxItems": 50,
				"items": {
					"type": "object",
					"properties": {
						"content":    {"type": "string", "description": "Imperative description of the task."},
						"status":     {"type": "string", "enum": ["pending", "in_progress", "completed"], "description": "Task status."},
						"activeForm": {"type": "string", "description": "Present-continuous form; meaningful only for in_progress items."},
						"level":      {"type": "integer", "enum": [0, 1], "description": "0 = milestone (PHASE), 1 = sub-step under a milestone."}
					},
					"required": ["content", "status"],
					"additionalProperties": false
				}
			}
		},
		"required": ["todos"],
		"additionalProperties": false
	}`)
}

func (t *todoWriteTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	raw, _ := args["todos"].([]any)
	if len(raw) > maxTodoItems {
		return Result{IsError: true, Content: fmt.Sprintf("todo_write: at most %d items, got %d", maxTodoItems, len(raw))}
	}
	items := make([]todoItem, 0, len(raw))
	for i, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("todo_write: todos[%d] must be an object", i)}
		}
		it, err := parseTodoItem(m)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("todo_write: todos[%d]: %v", i, err)}
		}
		items = append(items, it)
	}
	if err := validateTodoList(items); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	done := 0
	for _, it := range items {
		if it.Status == "completed" {
			done++
		}
	}
	return Result{Content: fmt.Sprintf("todo list updated: %d items (%d completed)", len(items), done)}
}

// parseTodoItem validates one list entry's own fields.
func parseTodoItem(m map[string]any) (todoItem, error) {
	var it todoItem
	it.Content = strings.TrimSpace(stringArg(m, "content"))
	if it.Content == "" {
		return it, errors.New("content is required and must be non-empty")
	}
	it.Status = stringArg(m, "status")
	switch it.Status {
	case "pending", "in_progress", "completed":
	default:
		return it, fmt.Errorf("status must be one of pending|in_progress|completed, got %q", it.Status)
	}
	it.ActiveForm = strings.TrimSpace(stringArg(m, "activeForm"))
	it.Level = intArg(m, "level", 0)
	if it.Level != 0 && it.Level != 1 {
		return it, fmt.Errorf("level must be 0 or 1, got %d", it.Level)
	}
	return it, nil
}

// validateTodoList enforces the cross-item invariants: at most one
// item in_progress, completed items carry no activeForm, and level-1
// sub-steps must follow a level-0 milestone.
func validateTodoList(items []todoItem) error {
	inProgress := 0
	for i, it := range items {
		if it.Status == "in_progress" {
			inProgress++
		}
		if it.Status == "completed" && it.ActiveForm != "" {
			return fmt.Errorf("todo list item %d: completed items cannot carry activeForm", i)
		}
		if it.Level == 1 && !hasMilestoneBefore(items, i) {
			return fmt.Errorf("todo list item %d: level 1 sub-step must follow a level 0 milestone", i)
		}
	}
	if inProgress > 1 {
		return errors.New("at most one item may be in_progress")
	}
	return nil
}

// hasMilestoneBefore reports whether a level-0 item precedes idx.
func hasMilestoneBefore(items []todoItem, idx int) bool {
	for j := 0; j < idx; j++ {
		if items[j].Level == 0 {
			return true
		}
	}
	return false
}

// completeStepTool signs off a planned step with evidence.
type completeStepTool struct{}

func (completeStepTool) Name() string { return "complete_step" }
func (completeStepTool) Description() string {
	return "Sign off a planned step with evidence. At least one evidence item is required — a completion claim without evidence is rejected. step_id references the todo list order; content echoes the step title."
}
func (completeStepTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"step_id": {"type": "integer", "description": "Index of the step in the todo list."},
			"content": {"type": "string", "description": "Title of the step being signed off."},
			"evidence": {
				"type": "array",
				"minItems": 1,
				"maxItems": 5,
				"items": {
					"type": "object",
					"properties": {
						"kind":        {"type": "string", "enum": ["verification", "diff", "test", "file", "manual"]},
						"description": {"type": "string", "description": "What was verified, which files changed, what was manually checked."}
					},
					"required": ["kind", "description"],
					"additionalProperties": false
				}
			}
		},
		"required": ["step_id", "content", "evidence"],
		"additionalProperties": false
	}`)
}

func (t *completeStepTool) Execute(_ context.Context, args map[string]any) Result {
	if err := validateArgs(t.Name(), args, t.Parameters()); err != nil {
		return Result{IsError: true, Content: err.Error()}
	}
	stepID := intArg(args, "step_id", 0)
	content := strings.TrimSpace(stringArg(args, "content"))
	if content == "" {
		return Result{IsError: true, Content: "content is required and must be non-empty"}
	}
	raw, _ := args["evidence"].([]any)
	if len(raw) == 0 {
		return Result{IsError: true, Content: "complete_step requires at least one evidence item"}
	}
	if len(raw) > maxEvidenceItems {
		return Result{IsError: true, Content: fmt.Sprintf("complete_step: at most %d evidence items, got %d", maxEvidenceItems, len(raw))}
	}
	ev := make([]stepEvidence, 0, len(raw))
	for i, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("complete_step: evidence[%d] must be an object", i)}
		}
		kind := stringArg(m, "kind")
		switch kind {
		case "verification", "diff", "test", "file", "manual":
		default:
			return Result{IsError: true, Content: fmt.Sprintf("complete_step: evidence[%d].kind must be one of verification|diff|test|file|manual, got %q", i, kind)}
		}
		desc := strings.TrimSpace(stringArg(m, "description"))
		if desc == "" {
			return Result{IsError: true, Content: fmt.Sprintf("complete_step: evidence[%d].description is required and must be non-empty", i)}
		}
		ev = append(ev, stepEvidence{Kind: kind, Description: desc})
	}
	return Result{Content: fmt.Sprintf("step %d %q signed off with %d evidence item(s)", stepID, content, len(ev))}
}

func init() {
	RegisterBuiltinFactory("todo_write", func(_ BuiltinConfig) (Tool, error) {
		return &todoWriteTool{}, nil
	})
	RegisterBuiltinFactory("complete_step", func(_ BuiltinConfig) (Tool, error) {
		return &completeStepTool{}, nil
	})
}
