package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
	"darvin-cowork/backend/internal/logger"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
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
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	log := logger.Get()
	defer log.Sync()

	log.Info("starting application", zap.String("name", cfg.App.Name), zap.String("env", cfg.App.Env))

	dbCfg := &database.Config{
		DSN: cfg.Database.DSN,
	}
	if err := database.Init(dbCfg); err != nil {
		log.Error("failed to init database", zap.Error(err))
		os.Exit(1)
	}
	log.Info("database initialized", zap.String("dsn", cfg.Database.DSN))

	log.Info("application started successfully")
}
