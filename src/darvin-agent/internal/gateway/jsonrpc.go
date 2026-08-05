// Package gateway implements the WebSocket + JSON-RPC 2.0 front door of the
// Go agent. It owns three components (see docs/系统架构.md §Gateway 层):
//
//	Server        — binds localhost:0, prints the port on stdout, serves /ws
//	SessionManager — nanoid session ids, in-memory sessionID → *session.Session
//	EventLedger   — sessionID → subscribed clients, pushes agent.event notifications
package gateway

import "encoding/json"

// JSONRPCVersion is the only "jsonrpc" value this server accepts or emits.
const JSONRPCVersion = "2.0"

// JSON-RPC 2.0 reserved error codes. Application-defined codes
// live in the -32000..-32099 range.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	// CodeSessionStalled: prompt 落在 Stop 之后的拒绝窗口内。
	// 见 SessionManager.Stop / stoppedUntilMs。
	CodeSessionStalled = -32001

	// CodeNoAgentLoopSession: handler 命中一个 entry 但其 AgentLoopSession 还没建
	//(subscribe 早于 prompt,且 SessionManager 未注入 factory)。
	// 正常生产路径不会触发;只用于 handler 测试 stub。
	CodeNoAgentLoopSession = -32002

	// CodeAgentInitFailed: factory.NewAgentLoopSession 构造失败,SessionManager
	// 已回滚 entry;renderer 可重试。
	CodeAgentInitFailed = -32003

	// CodeSkillNotFound: agent.skill.invoke_user 命中不存在的 skill。
	CodeSkillNotFound = -32010

	// CodeSkillDisabled: agent.skill.invoke_user 命中已禁用的 skill。
	CodeSkillDisabled = -32011

	// CodeSkillNotUserInvocable: agent.skill.invoke_user 命中
	// userInvocable=false 的 skill。
	CodeSkillNotUserInvocable = -32012
)

// Request is an inbound JSON-RPC call. ID is kept as raw JSON because the
// spec allows string, number or null, and a response must echo it verbatim.
// A request with no ID is a notification and gets no response.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request omitted an id, in which case
// the server must not reply.
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

// nullID is the id used when a request could not be parsed far enough to
// recover its id (JSON-RPC 2.0 §5: "If there was an error in detecting the
// id ... it MUST be Null").
var nullID = json.RawMessage("null")

// parseFrame decodes one WebSocket text frame. A frame is either a single
// request object or a batch array. batch is true when the payload was an
// array, which determines whether the reply is an array too.
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
