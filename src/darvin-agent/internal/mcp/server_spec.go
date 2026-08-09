// ServerSpec is the user-facing configuration for one MCP server, plus
// the transport selector that decides how it is reached.

package mcp

// TransportType picks how a server is reached. stdio is the dominant
// case; http covers the streamable-HTTP transport; sse is reserved for
// legacy servers.
type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportHTTP  TransportType = "http"
	TransportSSE   TransportType = "sse"
)

// ServerSpec is the user-facing description of one MCP server. The Go
// side never mutates it; the registry copies into a serverEntry.
//
// JSON tags mirror the main-side wire contract (camelCase). Without
// them, encoding/json's case-insensitive lookup can only match fields
// whose Go name differs by case.
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
