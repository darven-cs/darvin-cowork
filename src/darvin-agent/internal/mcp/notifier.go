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
}

type ConnectionStatus string

const (
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionConnecting  ConnectionStatus = "connecting"
	ConnectionConnected   ConnectionStatus = "connected"
	ConnectionError       ConnectionStatus = "error"
)

// noopNotifier silences both callbacks. Used as the zero-value default
// so callers that never call SetNotifier get a no-op registry.
func noopNotifier() Notifier {
	return Notifier{
		OnConnectionChanged: func(string, ConnectionStatus, string) {},
		OnResolutionChanged: func(string, LaunchResolution) {},
	}
}
