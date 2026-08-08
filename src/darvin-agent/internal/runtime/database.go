package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
)

// loadDatabase opens the SQLite database, runs the schema migration,
// and constructs the four store handles the runtime owns.
func loadDatabase(_ context.Context, cfg *config.Config, log *zap.Logger) (Stores, error) {
	dsn, err := config.ResolveSessionsDSN(cfg.Database.SessionsDSN)
	if err != nil {
		return Stores{}, fmt.Errorf("resolve sessions dsn: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
		return Stores{}, fmt.Errorf("create sessions dir: %w", err)
	}
	if err := database.Init(&database.Config{SessionsDSN: dsn}); err != nil {
		return Stores{}, fmt.Errorf("init database: %w", err)
	}
	log.Info("database initialized", zap.String("sessions_dsn", dsn))

	if err := database.AutoMigrate(
		&store.Session{},
		&store.Message{},
		&store.SessionDigest{},
		&store.SkillSnapshot{},
		&store.AppState{},
		&store.ImportedFile{},
		&store.SessionUsage{},
	); err != nil {
		return Stores{}, fmt.Errorf("auto migrate: %w", err)
	}
	log.Info("database migrated")

	db := database.Get()
	return Stores{
		Sessions:      store.NewSQLiteStore(db),
		Messages:      store.NewSQLiteMessageStore(db),
		AppState:      store.NewAppStateStore(db),
		ImportedFiles: store.NewImportedFileStore(db),
		Usages:        store.NewSQLiteUsageStore(db),
	}, nil
}

