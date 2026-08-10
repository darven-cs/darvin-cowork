// Package gateway implements the WebSocket + JSON-RPC 2.0 front door of
// the Go agent (Server, SessionManager, EventLedger).
package gateway

import "encoding/json"

// JSONRPCVersion is the only "jsonrpc" value this server accepts or emits.
const JSONRPCVersion = "2.0"

// JSON-RPC 2.0 reserved error codes; application codes use -32000..-32099.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeSessionStalled: prompt inside the refusal window after Stop.
	CodeSessionStalled = -32001

	// CodeNoSessionRuntime: handler hit an entry whose
	// SessionRuntime is not built (subscribe preceded prompt).
	CodeNoSessionRuntime = -32002

	// CodeAgentInitFailed: lazy build failed; renderer may retry.
	CodeAgentInitFailed = -32003

	// agent.skill.invoke_user outcome codes.
	CodeSkillNotFound         = -32010
	CodeSkillDisabled         = -32011
	CodeSkillNotUserInvocable = -32012
)

// Request is an inbound JSON-RPC call. ID is raw JSON so the response
// can echo it verbatim (string / number / null). No id ⇒ notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request omitted its id (the
// server must not reply to notifications).
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response is an outbound reply. Exactly one of Result / Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a server-initiated message with no id, used to push
// agent events to the client.
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Message }

// successResp builds a result response echoing id.
func successResp(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: JSONRPCVersion, ID: id, Result: result}
}

// errorResp builds an error response echoing id.
func errorResp(id json.RawMessage, code int, message string, data any) *Response {
	return &Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error:   &RPCError{Code: code, Message: message, Data: data},
	}
}

// newNotification builds a server-push notification.
func newNotification(method string, params any) *Notification {
	return &Notification{JSONRPC: JSONRPCVersion, Method: method, Params: params}
}

// nullID is the fallback id when the request's id cannot be recovered
// (JSON-RPC 2.0 §5: unparseable id MUST be Null).
var nullID = json.RawMessage("null")

// parseFrame decodes one WebSocket text frame (single request or batch
// array). batch=true ⇒ reply is also an array.
func parseFrame(data []byte) (reqs []*Request, batch bool, err error) {
	trimmed := skipSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []*Request
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, true, err
		}
		return arr, true, nil
	}
	var single Request
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, false, err
	}
	return []*Request{&single}, false, nil
}

func skipSpace(b []byte) []byte {
	i := 0
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return b[i:]
		}
	}
	return b[i:]
}
