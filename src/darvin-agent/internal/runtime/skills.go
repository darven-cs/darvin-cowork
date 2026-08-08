package runtime

import (
	"context"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/skills"
	tool "darvin-cowork/backend/internal/tools"
)

// bootstrapSkills loads the project + global skills registries. The
// runner resolves skill execution contexts against toolsReg so the
// built-in tools are available to skill turns out of the box.
func bootstrapSkills(_ context.Context, log *zap.Logger, workspace string, toolsReg *tool.Registry) *skills.BootstrapResult {
	return skills.Bootstrap(context.Background(), log, skills.BootstrapConfig{
		ProjectSkillsDir: workspace,
		GlobalConfigDir:  skills.DefaultGlobalConfigDir(),
		ToolReg:          toolsReg,
	})
}
