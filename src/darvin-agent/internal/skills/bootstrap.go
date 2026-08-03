package skills

import (
	"context"
	"embed"
	"path/filepath"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/tools"
)

// Bundle describes the embedded SKILL.md tree. main.go wires the
// //go:embed directive in cmd/app.
type Bundle struct {
	FS  embed.FS
	Dir string
}

// BootstrapConfig wires skills to the rest of the agent runtime.
type BootstrapConfig struct {
	Bundle      Bundle
	UserDataDir string
	ToolReg     *tool.Registry
}

// BootstrapResult is the runtime handle other components use to read the
// registry and run skills.
type BootstrapResult struct {
	Registry *SkillRegistry
	Runner   *SkillRunner
}

// Bootstrap loads bundled + user skills, logs a summary, and returns a
// registry + runner. Errors are logged and never abort agent startup.
func Bootstrap(ctx context.Context, log *zap.Logger, cfg BootstrapConfig) *BootstrapResult {
	reg := NewSkillRegistry()
	sources := make([]SkillSourceLoader, 0, 2)
	if cfg.Bundle.FS != (embed.FS{}) {
		sources = append(sources, &BundledSource{FS: cfg.Bundle.FS, Dir: cfg.Bundle.Dir})
	}
	if cfg.UserDataDir != "" {
		sources = append(sources, &UserSource{RootDir: filepath.Join(cfg.UserDataDir, "SKILLs")})
	}
	if err := reg.Load(ctx, sources); err != nil {
		if log != nil {
			log.Warn("skills: load failed", zap.Error(err))
		}
	}
	entries := reg.Snapshot()
	bundled := 0
	user := 0
	for _, e := range entries {
		switch e.Source {
		case SkillSourceBundled:
			bundled++
		case SkillSourceUser:
			user++
		}
	}
	if log != nil {
		log.Info("skills: loaded",
			zap.Int("bundled", bundled),
			zap.Int("user", user),
			zap.Int("total", len(entries)))
	}
	runner := NewSkillRunner(reg, cfg.ToolReg)
	return &BootstrapResult{Registry: reg, Runner: runner}
}
