package gateway

import (
	"context"
	"encoding/json"
	"strings"
)

// PromptParams is the JSON-RPC params for agent.prompt. sessionId is
// optional; when omitted the gateway allocates a new session.
type PromptParams struct {
	Content   string `json:"content"`
	SessionID string `json:"sessionId,omitempty"`
}

// PromptResult is the JSON-RPC result for agent.prompt. sessionId and
// messageId are 21-char nanoids with no prefix (the spec rejected the
// s-/m- scheme because the WS frame is opaque to the renderer).
type PromptResult struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
}

// AbortParams is the JSON-RPC params for agent.abort. S3 returns a
// stub success unconditionally — the ACP loop (S4) is what wires
// actual cancellation through to Agent.Prompt.
type AbortParams struct {
	SessionID string `json:"sessionId"`
}

// AbortResult is the JSON-RPC result for agent.abort.
type AbortResult struct {
	Aborted   bool   `json:"aborted"`
	SessionID string `json:"sessionId"`
}

// SubscribeEventsParams is the JSON-RPC params for agent.subscribe_events.
// sessionId must reference an existing session (i.e. one created earlier
// on the same WS connection); unknown ids are rejected with -32602.
type SubscribeEventsParams struct {
	SessionID string `json:"sessionId"`
}

// SubscribeEventsResult is the JSON-RPC result for agent.subscribe_events.
type SubscribeEventsResult struct {
	Subscribed bool `json:"subscribed"`
}

// dispatchRequest routes one parsed JSON-RPC request to the matching
// handler. Nil return signals "notification — do not reply" (we don't
// accept any notifications in S3; the switch handles that by always
// returning a value).
func dispatchRequest(ctx context.Context, req *Request, c *client) *Response {
	switch req.Method {
	case "agent.prompt":
		return handlePrompt(ctx, req.ID, req.Params, c)
	case "agent.abort":
		return handleAbort(ctx, req.ID, req.Params, c)
	case "agent.subscribe_events":
		return handleSubscribeEvents(ctx, req.ID, req.Params, c)
	default:
		return errorResp(req.ID, CodeMethodNotFound,
			"Method not found: "+req.Method, nil)
	}
}

func handlePrompt(_ context.Context, id json.RawMessage, params json.RawMessage, c *client) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p PromptParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if strings.TrimSpace(p.Content) == "" {
		return errorResp(id, CodeInvalidParams, "content is required", nil)
	}

	sess, msgID, _ := c.sessions.CreateOrGet(p.SessionID)
	// Kick the stub pipeline off in the background. EmitStub publishes
	// a text_delta + agent_end pair; the renderer can then drain.
	c.ledger.EmitStub(sess.ID, msgID, p.Content)
	return successResp(id, PromptResult{SessionID: sess.ID, MessageID: msgID})
}

func handleAbort(_ context.Context, id json.RawMessage, params json.RawMessage, c *client) *Response {
	var p AbortParams
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	// S3 stub: always reports "aborted: true". S4 will hook the real
	// acp.Loop.SignalAborted through here.
	return successResp(id, AbortResult{Aborted: true, SessionID: p.SessionID})
}

func handleSubscribeEvents(_ context.Context, id json.RawMessage, params json.RawMessage, c *client) *Response {
	if len(params) == 0 {
		return errorResp(id, CodeInvalidParams, "params required", nil)
	}
	var p SubscribeEventsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResp(id, CodeInvalidParams, "params must be an object", nil)
	}
	if p.SessionID == "" {
		return errorResp(id, CodeInvalidParams, "sessionId is required", nil)
	}
	if !c.sessions.Has(p.SessionID) {
		return errorResp(id, CodeInvalidParams, "unknown sessionId", nil)
	}
	c.ledger.Subscribe(p.SessionID, c)
	return successResp(id, SubscribeEventsResult{Subscribed: true})
}
