package runtime

import (
	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
	tool "darvin-cowork/backend/internal/tools"
)

// loadTools constructs the built-in tool registry once. The same
// registry is shared by the per-session factory and the skills
// runner so tool surface stays consistent.
func loadTools(workspace string, cfg *config.Config, log *zap.Logger) (*tool.Registry, error) {
	toolsReg, err := tool.NewBuiltins(workspace, cfg.Agent.ShellAllowlist)
	if err != nil {
		log.Warn("skills: tool registry init failed, using empty registry", zap.Error(err))
		toolsReg = tool.NewRegistry()
	}
	return toolsReg, nil
}
