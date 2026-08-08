// Bootstraps the MCP registry and its package root at startup.

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/mcp"
)

// bootstrapMCP constructs the MCP registry under the absolute
// mcp-packages root (resolved by resolveMCPPackagesDir; production
// injects the user-data path via DARVIN_MCP_PACKAGES_DIR, dev falls
// back to os.UserCacheDir) and scans for stale resolutions from a
// previous run. The root is deliberately NOT derived from the
// per-session workspace — mcp installs are app-level data and must
// outlive session rotation.
func bootstrapMCP(ctx context.Context, log *zap.Logger, mcpRoot string) (*mcp.Registry, error) {
	if !filepath.IsAbs(mcpRoot) {
		return nil, fmt.Errorf("bootstrapMCP: mcp-packages root must be absolute: %q", mcpRoot)
	}
	if err := os.MkdirAll(mcpRoot, 0o755); err != nil {
		log.Warn("mcp packages dir create failed", zap.Error(err))
	}
	resolver := mcp.NewResolverManager(mcpRoot).WithLogger(log)
	registry := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence()).WithLogger(log)
	if err := registry.LoadStaleResolutions(ctx); err != nil {
		log.Warn("mcp stale resolution scan failed", zap.Error(err))
	}
	return registry, nil
}
