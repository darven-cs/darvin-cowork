// Wire envelope: the JSON-RPC 2.0 message shapes the client exchanges
// over a transport. Types here are transport-agnostic; framing lives in
// the transport subpackage.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ProtocolVersion is the MCP protocol version announced in initialize.
const ProtocolVersion = "2024-11-05"

var (
	ErrTransportClosed    = errors.New("mcp: transport closed")
	ErrMethodNotFound     = errors.New("mcp: method not found")
	ErrRPCMaxRetries      = errors.New("mcp: max retries exceeded")
	ErrNoReconnectFactory = errors.New("mcp: no reconnect factory configured")
)

// RPCError mirrors the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// Request is one outbound JSON-RPC call. ID is a monotonically increasing
// int64; reference MCP servers all return numbers, so int64 keeps the
// type narrow.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is one inbound message. Exactly one of Result / Error is set.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// InitializeResult is the typed payload of the `initialize` response.
type InitializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      ServerInfo     `json:"serverInfo"`
}

// ServerInfo is the identifying block returned by every MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
