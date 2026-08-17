// GORM-backed AgentStore for the per-workspace agent rows.

package store

import (
	"context"
	"errors"

	"github.com/jaevor/go-nanoid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	agentIDLen    = 21
	agentAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var agentIDGen = nanoid.MustCustomASCII(agentAlphabet, agentIDLen)

// SQLiteAgentStore persists Agent rows on the shared *gorm.DB.
type SQLiteAgentStore struct {
	db *gorm.DB
}

// NewSQLiteAgentStore wraps the shared *gorm.DB.
func NewSQLiteAgentStore(db *gorm.DB) *SQLiteAgentStore {
	return &SQLiteAgentStore{db: db}
}

// Create inserts one agent row. An empty ID is minted here so handlers can
// build the row without knowing the id scheme.
func (s *SQLiteAgentStore) Create(ctx context.Context, agent Agent) (Agent, error) {
	if agent.ID == "" {
		agent.ID = agentIDGen()
	}
	if agent.Source == "" {
		agent.Source = "user"
	}
	if agent.Color == "" {
		agent.Color = "blue"
	}
	if err := s.db.WithContext(ctx).Create(&agent).Error; err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// Update saves the full row by primary key.
func (s *SQLiteAgentStore) Update(ctx context.Context, agent Agent) error {
	return s.db.WithContext(ctx).Save(&agent).Error
}

// Delete removes the row by id; idempotent (no error on missing row).
func (s *SQLiteAgentStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&Agent{}).Error
}

// GetByID returns one agent row, or ErrNotFound.
func (s *SQLiteAgentStore) GetByID(ctx context.Context, id string) (Agent, error) {
	var row Agent
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	return row, nil
}

// ListByWorkspace returns the workspace's agents ordered presets-first then
// by sort_order.
func (s *SQLiteAgentStore) ListByWorkspace(ctx context.Context, workspaceID string) ([]Agent, error) {
	var rows []Agent
	err := s.db.WithContext(ctx).
		Where("workspace_id = ?", workspaceID).
		Order("is_default desc, source asc, sort_order asc").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// GetDefaultForWorkspace returns the workspace's is_default agent, or
// ErrNotFound when none is set.
func (s *SQLiteAgentStore) GetDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error) {
	var row Agent
	err := s.db.WithContext(ctx).
		Where("workspace_id = ? AND is_default = ?", workspaceID, true).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	return row, nil
}

// EnsureDefaultForWorkspace creates the Main Agent row (id
// "{workspaceID}/preset-main", is_default=true) when the workspace has no
// default; otherwise it returns the existing default untouched.
func (s *SQLiteAgentStore) EnsureDefaultForWorkspace(ctx context.Context, workspaceID string) (Agent, error) {
	if existing, err := s.GetDefaultForWorkspace(ctx, workspaceID); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Agent{}, err
	}
	main := MainAgentSeed()
	main.ID = workspaceID + "/" + main.PresetID
	main.WorkspaceID = workspaceID
	main.IsDefault = true
	if err := s.db.WithContext(ctx).Create(&main).Error; err != nil {
		return Agent{}, err
	}
	return main, nil
}

// SeedPresets inserts the 9 expert presets scoped to workspaceID. Row id is
// "{workspaceID}/{presetID}" so a re-run collides on the primary key and is
// skipped — the seed is idempotent per workspace.
func (s *SQLiteAgentStore) SeedPresets(ctx context.Context, workspaceID string) error {
	for _, preset := range PresetSeed() {
		row := preset
		row.ID = workspaceID + "/" + preset.PresetID
		row.WorkspaceID = workspaceID
		// INSERT ... ON CONFLICT DO NOTHING keeps re-runs idempotent
		// without a read-before-write per row.
		if err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
