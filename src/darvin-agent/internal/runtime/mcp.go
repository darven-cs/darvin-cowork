package runtime

import (
	"context"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/mcp"
)

// bootstrapMCP constructs the MCP registry under <workspace>/mcp-packages
// and scans for stale resolutions from a previous run.
func bootstrapMCP(ctx context.Context, log *zap.Logger, workspace string) (*mcp.Registry, error) {
	root := filepath.Join(workspace, "mcp-packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Warn("mcp packages dir create failed", zap.Error(err))
	}
	resolver := mcp.NewResolverManager(root).WithLogger(log)
	registry := mcp.NewRegistry(resolver, mcp.NewInMemoryResolutionPersistence()).WithLogger(log)
	if err := registry.LoadStaleResolutions(ctx); err != nil {
		log.Warn("mcp stale resolution scan failed", zap.Error(err))
	}
	return registry, nil
}
