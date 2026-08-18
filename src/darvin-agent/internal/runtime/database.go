// Opens the SQLite database and constructs the store handles the runtime owns.

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"darvin-cowork/backend/internal/agents/store"
	"darvin-cowork/backend/internal/config"
	"darvin-cowork/backend/internal/database"
)

// loadDatabase opens the SQLite database, runs the schema migration,
// and constructs the four store handles the runtime owns.
func loadDatabase(ctx context.Context, cfg *config.Config, log *zap.Logger) (Stores, error) {
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
		&store.Workspace{},
		&store.Agent{},
		&store.Message{},
		&store.SessionDigest{},
		&store.SkillSnapshot{},
		&store.AppState{},
		&store.ImportedFile{},
		&store.SessionUsage{},
		&store.Subagent{},
		&store.Schedule{},
		&store.ScheduleRun{},
	); err != nil {
		return Stores{}, fmt.Errorf("auto migrate: %w", err)
	}
	log.Info("database migrated")

	db := database.Get()
	agents := store.NewSQLiteAgentStore(db)
	if err := seedAgents(ctx, db, agents, log); err != nil {
		return Stores{}, fmt.Errorf("seed agents: %w", err)
	}

	return Stores{
		Sessions:      store.NewSQLiteStore(db),
		Workspaces:    store.NewSQLiteWorkspaceStore(db),
		Messages:      store.NewSQLiteMessageStore(db),
		AppState:      store.NewAppStateStore(db),
		ImportedFiles: store.NewImportedFileStore(db),
		Usages:        store.NewSQLiteUsageStore(db),
		Subagents:     store.NewSQLiteSubagentStore(db),
		Agents:        agents,
		Schedules:     store.NewScheduleStore(db),
	}, nil
}

// seedAgents backfills agent rows for workspaces created before the agent
// system existed. A fresh DB (no workspaces yet) is left alone — the first
// workspace arrives through agent.create_workspace, whose handler seeds
// presets + the Main Agent default inline.
func seedAgents(ctx context.Context, db *gorm.DB, agents *store.SQLiteAgentStore, log *zap.Logger) error {
	rows, err := store.NewSQLiteWorkspaceStore(db).List(ctx)
	if err != nil {
		return err
	}
	for _, w := range rows {
		if err := agents.SeedPresets(ctx, w.ID); err != nil {
			return err
		}
		main, err := agents.EnsureDefaultForWorkspace(ctx, w.ID)
		if err != nil {
			log.Warn("ensure default agent failed",
				zap.String("workspace_id", w.ID), zap.Error(err))
			continue
		}
		// Make sure the workspace's default_agent_id column agrees with
		// the agent row — older workspaces pre-date the column and need
		// a one-time backfill.
		if w.DefaultAgentID != main.ID {
			if err := store.NewSQLiteWorkspaceStore(db).
				UpdateDefaultAgent(ctx, w.ID, main.ID); err != nil {
				log.Warn("workspace default_agent_id backfill failed",
					zap.String("workspace_id", w.ID), zap.Error(err))
			}
		}
	}
	return nil
}
