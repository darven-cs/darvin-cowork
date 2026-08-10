// Defines the callbacks fired when a server's connection state changes.

package mcp

// Notifier carries the optional callbacks Registry fires when server
// runtime state changes. The handler in the gateway package implements
// both methods and re-emits the change as a JSON-RPC notification; v0
// callers without a Notifier (tests, the cmd binary during early init)
// get a no-op default.
//
// ConnectionStatus is a short string ("connecting" / "connected" /
// "error" / "disconnected") chosen by the registry; the gateway does
// not transform it.
type Notifier struct {
	OnConnectionChanged func(serverID string, status ConnectionStatus, errMsg string)
	OnResolutionChanged func(serverID string, res LaunchResolution)
	// OnToolsChanged fires when a server's tool list changes at runtime
	// (notifications/tools/list_changed); the handler re-applies plugins.
	OnToolsChanged func(serverID string)
}

// ConnectionStatus is the short lifecycle label Registry reports to the
// gateway on connection changes; the gateway re-emits it verbatim.
type ConnectionStatus string

const (
	// ConnectionDisconnected means the server has never connected or shut down.
	ConnectionDisconnected ConnectionStatus = "disconnected"
	// ConnectionConnecting means a dial is in progress.
	ConnectionConnecting ConnectionStatus = "connecting"
	// ConnectionConnected means the handshake succeeded.
	ConnectionConnected ConnectionStatus = "connected"
	// ConnectionError means the last dial or read failed.
	ConnectionError ConnectionStatus = "error"
)

// noopNotifier silences both callbacks. Used as the zero-value default
// so callers that never call SetNotifier get a no-op registry.
func noopNotifier() Notifier {
	return Notifier{
		OnConnectionChanged: func(string, ConnectionStatus, string) {},
		OnResolutionChanged: func(string, LaunchResolution) {},
		OnToolsChanged:      func(string) {},
	}
}
