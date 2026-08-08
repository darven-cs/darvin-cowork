// Resolves the config.yaml location and loads the runtime configuration.

package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/logger"
)

// resolveConfigPath picks the config.yaml location, in order:
//  1. $DARVIN_CONFIG, if set
//  2. <exe-dir>/config.yaml, the production layout
//  3. "config.yaml" — relative to cwd for `go run`
func resolveConfigPath() string {
	if p := os.Getenv("DARVIN_CONFIG"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "config.yaml")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "config.yaml"
}

// loadConfig loads config.yaml and initialises the zap logger. The
// returned logger is the one main / Build logs through; everything
// downstream takes *zap.Logger.
func loadConfig(cfgPath string) (*config.Config, *zap.Logger, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	logCfg := &logger.Config{
		Level:      cfg.Log.Level,
		Encoding:   cfg.Log.Encoding,
		Output:     cfg.Log.Output,
		Filename:   cfg.Log.Filename,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
	}
	if err := logger.Init(logCfg); err != nil {
		return nil, nil, fmt.Errorf("init logger: %w", err)
	}
	return cfg, logger.Get().Logger, nil
}
