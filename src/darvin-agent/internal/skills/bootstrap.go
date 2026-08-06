package skills

import (
	"context"
	"embed"
	"os"
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
	Bundle           Bundle
	ProjectSkillsDir string
	GlobalConfigDir  string
	ToolReg          *tool.Registry
}

// BootstrapResult is the runtime handle other components use to read the
// registry and run skills.
type BootstrapResult struct {
	Registry *SkillRegistry
	Runner   *SkillRunner
}

// Bootstrap loads skills from bundled + global + project sources, logs a
// summary, and returns a registry + runner. Errors are logged and never
// abort agent startup. The source list is ordered so the registry's
// last-loaded-wins dedup yields the intended priority:
// bundled < global < project.
func Bootstrap(ctx context.Context, log *zap.Logger, cfg BootstrapConfig) *BootstrapResult {
	reg := NewSkillRegistry()
	sources := make([]SkillSourceLoader, 0, 3)
	if cfg.Bundle.FS != (embed.FS{}) {
		sources = append(sources, &BundledSource{FS: cfg.Bundle.FS, Dir: cfg.Bundle.Dir})
	}
	if cfg.GlobalConfigDir != "" {
		sources = append(sources, &UserSource{
			RootDir:           filepath.Join(cfg.GlobalConfigDir, "skills"),
			Source:            SkillSourceGlobal,
			RequireFlatMarker: false,
		})
	}
	if cfg.ProjectSkillsDir != "" {
		sources = append(sources, &UserSource{
			RootDir:           filepath.Join(cfg.ProjectSkillsDir, "skills"),
			Source:            SkillSourceProject,
			RequireFlatMarker: false,
		})
	}
	if err := reg.Load(ctx, sources); err != nil {
		if log != nil {
			log.Warn("skills: load failed", zap.Error(err))
		}
	}
	entries := reg.Snapshot()
	bundled := 0
	project := 0
	global := 0
	for _, e := range entries {
		switch e.Source {
		case SkillSourceBundled:
			bundled++
		case SkillSourceProject:
			project++
		case SkillSourceGlobal:
			global++
		}
	}
	if log != nil {
		log.Info("skills: loaded",
			zap.Int("project", project),
			zap.Int("global", global),
			zap.Int("bundled", bundled),
			zap.Int("total", len(entries)))
	}
	runner := NewSkillRunner(reg, cfg.ToolReg)
	return &BootstrapResult{Registry: reg, Runner: runner}
}

// DefaultGlobalConfigDir returns the OS-recommended per-user config
// directory joined with "darvin-cowork/darvin-agent". Returns "" if the
// platform does not expose one (rare; only on misconfigured systems).
//
// Linux  : /home/<u>/.config/darvin-cowork/darvin-agent
// macOS  : /Users/<u>/Library/Application Support/darvin-cowork/darvin-agent
// Windows: C:\Users\<u>\AppData\Roaming\darvin-cowork\darvin-agent
func DefaultGlobalConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "darvin-cowork", "darvin-agent")
}