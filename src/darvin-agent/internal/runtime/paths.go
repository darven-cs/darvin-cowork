package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveMCPPackagesDir picks the absolute path for MCP npm install
// landing. Production wiring injects DARVIN_MCP_PACKAGES_DIR via the
// Electron main process (which gets it from app.getPath('userData')
// joined with 'darvin-agent/mcp-packages'). When the env var is missing
// (go run / tests), it falls back to the OS user cache dir so dev
// never writes into cwd.
//
// Returns an error when the resolved path is relative (the env var
// was set to a relative path, which historically meant "land in cwd
// and pollute the repo") or when neither source is available. Both
// cases fail loudly at Build time rather than silently corrupting the
// working tree.
func resolveMCPPackagesDir() (string, error) {
	if p := strings.TrimSpace(os.Getenv("DARVIN_MCP_PACKAGES_DIR")); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("DARVIN_MCP_PACKAGES_DIR must be absolute, got %q", p)
		}
		return p, nil
	}
	if c, err := os.UserCacheDir(); err == nil && c != "" {
		return filepath.Join(c, "darvin-cowork", "mcp-packages"), nil
	}
	return "", fmt.Errorf("DARVIN_MCP_PACKAGES_DIR not set and os.UserCacheDir unavailable")
}
