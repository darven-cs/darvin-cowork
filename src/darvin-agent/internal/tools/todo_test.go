// Tests for the todo_write / complete_step tools: schema and cross-item
// validation, evidence enforcement, and receipt output.

package tool

import (
	"context"
	"strings"
	"testing"
)

// todoItemArgs builds a flat todo item map.
func todoItemArgs(content, status string) map[string]any {
	return map[string]any{"content": content, "status": status}
}

func todoWriteArgs(items ...map[string]any) map[string]any {
	todos := make([]any, 0, len(items))
	for _, it := range items {
		todos = append(todos, it)
	}
	return map[string]any{"todos": todos}
}

func evidenceItemArgs(kind, desc string) map[string]any {
	return map[string]any{"kind": kind, "description": desc}
}

func completeStepArgs(stepID int, content string, ev ...map[string]any) map[string]any {
	evidence := make([]any, 0, len(ev))
	for _, e := range ev {
		evidence = append(evidence, e)
	}
	return map[string]any{"step_id": stepID, "content": content, "evidence": evidence}
}

func TestTodoWriteValidList(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(
		todoItemArgs("Design the parser", "completed"),
		todoItemArgs("Add the parser", "in_progress"),
		todoItemArgs("Write tests", "pending"),
	))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "3 items (1 completed)") {
		t.Fatalf("unexpected receipt: %q", res.Content)
	}
}

func TestTodoWriteEmptyListClears(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "0 items (0 completed)") {
		t.Fatalf("unexpected receipt: %q", res.Content)
	}
}

func TestTodoWriteRejectsTwoInProgress(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(
		todoItemArgs("Task A", "in_progress"),
		todoItemArgs("Task B", "in_progress"),
	))
	if !res.IsError {
		t.Fatal("expected error for two in_progress items")
	}
	if !strings.Contains(res.Content, "at most one item may be in_progress") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsActiveFormOnCompleted(t *testing.T) {
	item := todoItemArgs("Task A", "completed")
	item["activeForm"] = "Doing task A"
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(item))
	if !res.IsError {
		t.Fatal("expected error for activeForm on completed item")
	}
	if !strings.Contains(res.Content, "completed items cannot carry activeForm") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteAllowsActiveFormOnInProgress(t *testing.T) {
	item := todoItemArgs("Task A", "in_progress")
	item["activeForm"] = "Doing task A"
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(item))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
}

func TestTodoWriteLevelOneRequiresMilestoneBefore(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(
		withLevel(todoItemArgs("Sub step", "pending"), 1),
	))
	if !res.IsError {
		t.Fatal("expected error for level-1 sub-step before a milestone")
	}
	if !strings.Contains(res.Content, "level 1 sub-step must follow a level 0 milestone") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteLevelOneAfterMilestoneOK(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(
		withLevel(todoItemArgs("Milestone", "in_progress"), 0),
		withLevel(todoItemArgs("Sub step", "pending"), 1),
	))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2 items") {
		t.Fatalf("unexpected receipt: %q", res.Content)
	}
}

func TestTodoWriteRejectsBadStatus(t *testing.T) {
	item := todoItemArgs("Task A", "done")
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(item))
	if !res.IsError {
		t.Fatal("expected error for non-enum status")
	}
	if !strings.Contains(res.Content, `must be one of pending|in_progress|completed`) {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsEmptyContent(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(todoItemArgs("  ", "pending")))
	if !res.IsError {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(res.Content, "content is required") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsMissingTodos(t *testing.T) {
	res := (&todoWriteTool{}).Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing todos")
	}
	if !strings.Contains(res.Content, "missing required argument") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsUnknownArg(t *testing.T) {
	args := todoWriteArgs(todoItemArgs("Task A", "pending"))
	args["todo"] = args["todos"]
	res := (&todoWriteTool{}).Execute(context.Background(), args)
	if !res.IsError {
		t.Fatal("expected error for unknown top-level arg")
	}
	if !strings.Contains(res.Content, "unknown arguments") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsTooManyItems(t *testing.T) {
	items := make([]map[string]any, maxTodoItems+1)
	for i := range items {
		items[i] = todoItemArgs("Task", "pending")
	}
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(items...))
	if !res.IsError {
		t.Fatal("expected error for over-limit list")
	}
	if !strings.Contains(res.Content, "at most 50 items") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestTodoWriteRejectsBadLevel(t *testing.T) {
	item := withLevel(todoItemArgs("Task A", "pending"), 2)
	res := (&todoWriteTool{}).Execute(context.Background(), todoWriteArgs(item))
	if !res.IsError {
		t.Fatal("expected error for level outside 0|1")
	}
	if !strings.Contains(res.Content, "level must be 0 or 1") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestCompleteStepValidEvidence(t *testing.T) {
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(
		1, "Add the parser",
		evidenceItemArgs("diff", "parser.go: +42 lines"),
		evidenceItemArgs("test", "go test ./parser passed"),
	))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, `step 1 "Add the parser" signed off with 2 evidence item(s)`) {
		t.Fatalf("unexpected receipt: %q", res.Content)
	}
}

func TestCompleteStepRejectsEmptyEvidence(t *testing.T) {
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(1, "Add the parser"))
	if !res.IsError {
		t.Fatal("expected error for empty evidence")
	}
	if !strings.Contains(res.Content, "requires at least one evidence item") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestCompleteStepRejectsBadKind(t *testing.T) {
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(
		1, "Add the parser", evidenceItemArgs("review", "peer reviewed"),
	))
	if !res.IsError {
		t.Fatal("expected error for non-enum kind")
	}
	if !strings.Contains(res.Content, "must be one of verification|diff|test|file|manual") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestCompleteStepRejectsEmptyDescription(t *testing.T) {
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(
		1, "Add the parser", evidenceItemArgs("diff", "  "),
	))
	if !res.IsError {
		t.Fatal("expected error for empty description")
	}
	if !strings.Contains(res.Content, "description is required") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestCompleteStepRejectsEmptyContent(t *testing.T) {
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(1, "   ", evidenceItemArgs("test", "passes")))
	if !res.IsError {
		t.Fatal("expected error for empty content")
	}
	if !strings.Contains(res.Content, "content is required") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

func TestCompleteStepRejectsTooManyEvidence(t *testing.T) {
	ev := make([]map[string]any, maxEvidenceItems+1)
	for i := range ev {
		ev[i] = evidenceItemArgs("manual", "checked")
	}
	res := (&completeStepTool{}).Execute(context.Background(), completeStepArgs(1, "Task", ev...))
	if !res.IsError {
		t.Fatal("expected error for over-limit evidence")
	}
	if !strings.Contains(res.Content, "at most 5 evidence items") {
		t.Fatalf("unexpected message: %q", res.Content)
	}
}

// withLevel attaches a level to a todo item map.
func withLevel(m map[string]any, level int) map[string]any {
	m["level"] = level
	return m
}
