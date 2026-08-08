// Package mcp implements the Go side of the Model Context Protocol: a
// JSON-RPC 2.0 client that talks to MCP servers over either a stdio
// child process (LSP-style Content-Length framing) or a plain HTTP POST.
// The transport lives in the transport subpackage; this file only deals
// with the wire envelope and the typed tool descriptors.
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ProtocolVersion is the MCP protocol version this client announces in
// the initialize handshake. Bump only when MCP server compatibility
// requires it.
const ProtocolVersion = "2024-11-05"

var (
	ErrTransportClosed    = errors.New("mcp: transport closed")
	ErrMethodNotFound     = errors.New("mcp: method not found")
	ErrRPCMaxRetries      = errors.New("mcp: max retries exceeded")
	ErrNoReconnectFactory = errors.New("mcp: no reconnect factory configured")
)

// RPCError mirrors the JSON-RPC 2.0 error object. Code uses the standard
// reserved range (-32700..-32603) plus the application-defined range
// -32000..-32099.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// Request is one outbound JSON-RPC call. ID is a monotonically increasing
// int64 chosen by the client; servers echo it back so the client can match
// responses to requests. We use int64 (not json.RawMessage) because the
// reference MCP servers all return numbers — keeping the type narrow
// avoids the gateway-style "raw id" allocation overhead.
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

// ToolDescriptor is one tool the server exposes via `tools/list`. The
// input schema is left as a generic map because the registry / tool
// surface will turn it into a typed schema at registration time.
type ToolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// CallToolResult is the typed payload of the `tools/call` response. The
// MCP spec allows a richer content union (text / image / audio / resource);
// 后续会 widen this when a real server needs more than text.
type CallToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is one block inside CallToolResult.Content. Type is a
// discriminator the renderer uses to pick a UI affordance.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// TransportType picks how a server is reached. stdio is the dominant case
// (MCP reference servers all spawn a subprocess); http covers the new
// streamable-HTTP transport; sse is reserved for legacy servers and
// not yet wired up (the HTTP transport accepts the SSE Accept header but
// does not parse event frames).
type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)

// ResolverKind classifies how a server's command is optimised before
// launch. npx is the only fully implemented kind; the rest are stubbed
// to return "unsupported" so the registry falls back to the raw command.
type ResolverKind string

const (
	ResolverNpx ResolverKind = "npx"
	ResolverUvx ResolverKind = "uvx"
	ResolverGo  ResolverKind = "go"
	ResolverRaw ResolverKind = "raw"
)

// ResolutionStatus is the lifecycle of a launch optimisation. The UI
// reads this verbatim; the strings are part of the IPC contract.
type ResolutionStatus string

const (
	StatusPending     ResolutionStatus = "pending"
	StatusInstalling  ResolutionStatus = "installing"
	StatusReady       ResolutionStatus = "ready"
	StatusFailed      ResolutionStatus = "failed"
	StatusUnsupported ResolutionStatus = "unsupported"
)

// ServerSpec is the user-facing description of one MCP server. The Go
// side never mutates this — the registry copies it into a serverEntry
// and uses the copy for everything.
//
// JSON tags mirror the main-side wire contract (camelCase). Without them,
// encoding/json's case-insensitive lookup can only match fields whose Go
// name differs by case (Transport <-> transportType are different names).
type ServerSpec struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Enabled     bool              `json:"enabled"`
	Transport   TransportType     `json:"transportType"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	IsBuiltIn   bool              `json:"isBuiltIn"`
	GitHubURL   string            `json:"githubUrl,omitempty"`
	RegistryID  string            `json:"registryId,omitempty"`
}

// StartupFailure describes a structured launch failure. It is constructed
// by the launcher when a server fails to start and is stored (with
// credentials redacted) on LaunchResolution so the UI can show the user
// a meaningful diagnostic without leaking secrets into logs.
type StartupFailure struct {
	// Stage is the phase in which the failure occurred.
	Stage string
	// Elapsed is how much time passed before the failure.
	Elapsed time.Duration
	// Stderr is the content the server wrote to stderr before failing.
	// Credentials are stripped by RedactCredentials before assignment.
	Stderr string
	// Err is the underlying Go error; stored as string so it survives
	// JSON serialization without custom marshaling.
	Err string
}

// LaunchResolution is the result of running a resolver. When Status is
// ready, Command/Args/Env are the optimised launch line; when Status is
// failed or unsupported, the registry falls back to ServerSpec.Command.
type LaunchResolution struct {
	ServerID          string
	ResolverKind      ResolverKind
	SourceFingerprint string
	Status            ResolutionStatus
	PackageName       string
	RequestedVersion  string
	ResolvedVersion   string
	InstallDir        string
	Command           string
	Args              []string
	Env               map[string]string
	Error             string
	// FailureStage records which phase a launch failure occurred in
	// (e.g. "resolve", "install", "spawn", "initialize"). Used by the UI
	// to show a structured diagnostic.
	FailureStage string
	// FailureElapsed records how much time passed before the failure.
	FailureElapsed time.Duration
	// FailureStderr records the last few KB of the server's stderr output
	// at the time of failure, with credentials stripped.
	FailureStderr string
	InstalledAt   time.Time
	ResolvedAt    time.Time
	UpdatedAt     time.Time
}

// ServerStatus is the read-only snapshot the renderer consumes.
// It bundles the spec-shaped view with the runtime state (connected,
// tools, error) so the renderer does not have to thread two structs.
type ServerStatus struct {
	ServerID        string
	Enabled         bool
	Resolving       bool
	Resolution      *LaunchResolution
	Connected       bool
	ConnectionError string
	Tools           []ToolDescriptor
}

// credentialRE is a pre-compiled regex that matches common credential shapes.
// The patterns are ordered so that more specific (longer) patterns come first,
// and the replacer map maps the capture group index to the placeholder.
var credentialRE = regexp.MustCompile(
	// Longest-first: avoid partial matches of short prefixes.
	`ghp_[A-Za-z0-9]{36}|` +
		`gho_[A-Za-z0-9]{36}|` +
		`github_pat_[A-Za-z0-9_]{22,}|` +
		`xox[baprs]-[A-Za-z0-9_]{10,}|` +
		`sk-[A-Za-z0-9_]{20,}|` +
		`Bearer [A-Za-z0-9_.-]{20,}|` +
		`Basic [A-Za-z0-9_.-]{10,}|` +
		`AKIA[A-Z0-9]{16}|` +
		// Generic long tokens last.
		`[A-Za-z0-9+/]{40,}={0,2}|` +
		`[0-9a-f]{32,}`,
)

// credentialReplacer maps specific substrings to their placeholders.
var credentialReplacer = regexp.MustCompile(
	`(ghp_[A-Za-z0-9]{36})|` +
		`(gho_[A-Za-z0-9]{36})|` +
		`(github_pat_[A-Za-z0-9_]{22,})|` +
		`(xox[baprs]-[A-Za-z0-9_]{10,})|` +
		`(sk-[A-Za-z0-9_]{20,})|` +
		`(Bearer [A-Za-z0-9_.-]{20,})|` +
		`(Basic [A-Za-z0-9_.-]{10,})|` +
		`(AKIA[A-Z0-9]{16})|` +
		`([A-Za-z0-9+/]{40,}={0,2})|` +
		`([0-9a-f]{32,})`,
)

// credentialPlaceholder returns the placeholder for a matched credential.
func credentialPlaceholder(match string) string {
	switch {
	case strings.HasPrefix(match, "ghp_") || strings.HasPrefix(match, "gho_") || strings.HasPrefix(match, "github_pat_"):
		return "[GITHUB_TOKEN]"
	case strings.HasPrefix(match, "xox"):
		return "[SLACK_TOKEN]"
	case strings.HasPrefix(match, "sk-"):
		return "[OPENAI_KEY]"
	case strings.HasPrefix(match, "Bearer "):
		return "[BEARER_TOKEN]"
	case strings.HasPrefix(match, "Basic "):
		return "[BASIC_AUTH]"
	case strings.HasPrefix(match, "AKIA"):
		return "[AWS_KEY]"
	default:
		return "[CREDENTIAL]"
	}
}

// RedactCredentials replaces credential-shaped strings in s with a placeholder.
// It is applied to stderr output and error messages before they are stored in
// LaunchResolution so that secrets never appear in logs or the UI.
func RedactCredentials(s string) string {
	// Use a simple approach: find matches and replace them one by one.
	result := []byte{}
	last := 0
	for _, match := range credentialRE.FindAllStringIndex(s, -1) {
		result = append(result, s[last:match[0]]...)
		result = append(result, credentialPlaceholder(s[match[0]:match[1]])...)
		last = match[1]
	}
	result = append(result, s[last:]...)
	return string(result)
}
