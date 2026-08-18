// SQLite-backed DAO for IM channel instances. Sharing the *gorm.DB with
// sessions / messages is intentional — see internal/database for the
// singleton rationale.

package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// IMChannel is one configured IM connector instance (credential + policy +
// enabled flag) persisted across restarts.
type IMChannel struct {
	ID          string `gorm:"primaryKey"`
	WorkspaceID string `gorm:"index;default:''"`
	Channel     string `gorm:"index"` // qq | wecom | weixin
	Name        string
	Enabled     bool   `gorm:"index;default:false"`
	ConfigJSON  string `gorm:"type:text"` // channel-specific credential payload
	AccessMode  string // open | allowlist | disabled
	AllowFrom   string `gorm:"type:text"` // comma-separated allowlist
	CreatedAt   int64  `gorm:"not null"`
	UpdatedAt   int64  `gorm:"not null"`
}

// TableName pins the IM channel table.
func (IMChannel) TableName() string { return "im_channels" }

// IMChannelToken persists a token (e.g. weixin bot_token) per instance so
// a restart skips re-scan.
type IMChannelToken struct {
	ID        string `gorm:"primaryKey"`
	Channel   string `gorm:"index"`
	TokenJSON string `gorm:"type:text"`
	UpdatedAt int64  `gorm:"not null"`
}

// TableName pins the token table.
func (IMChannelToken) TableName() string { return "im_channel_tokens" }

// ErrIMChannelNotFound is returned by Get / Delete / Toggle when no row
// matches the requested id.
var ErrIMChannelNotFound = errors.New("im channel not found")

// IMChannelStore wraps GORM CRUD for IMChannel / IMChannelToken.
type IMChannelStore struct {
	db *gorm.DB
}

// NewIMChannelStore builds an IMChannelStore against db. Caller owns db
// lifecycle; the store does not Close it.
func NewIMChannelStore(db *gorm.DB) *IMChannelStore {
	return &IMChannelStore{db: db}
}

// Create inserts an instance.
func (s *IMChannelStore) Create(ctx context.Context, ch *IMChannel) error {
	now := time.Now().UnixMilli()
	if ch.CreatedAt == 0 {
		ch.CreatedAt = now
	}
	ch.UpdatedAt = now
	return s.db.WithContext(ctx).Create(ch).Error
}

// Get returns one instance by id; ErrIMChannelNotFound when absent.
func (s *IMChannelStore) Get(ctx context.Context, id string) (IMChannel, error) {
	var row IMChannel
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return IMChannel{}, ErrIMChannelNotFound
		}
		return IMChannel{}, err
	}
	return row, nil
}

// List returns instances for a workspace, or every instance when
// workspaceID is empty (IM instances are app-global, not chat-scoped).
// Ordered by created_at asc.
func (s *IMChannelStore) List(ctx context.Context, workspaceID string) ([]IMChannel, error) {
	q := s.db.WithContext(ctx)
	if workspaceID != "" {
		q = q.Where("workspace_id = ?", workspaceID)
	}
	var rows []IMChannel
	if err := q.Order("created_at asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// Update applies a partial patch keyed by column name.
func (s *IMChannelStore) Update(ctx context.Context, id string, patch map[string]any) error {
	patch["updated_at"] = time.Now().UnixMilli()
	return s.db.WithContext(ctx).
		Model(&IMChannel{}).
		Where("id = ?", id).
		Updates(patch).Error
}

// Delete removes an instance.
func (s *IMChannelStore) Delete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).
		Where("id = ?", id).
		Delete(&IMChannel{}).Error
}

// SetEnabled flips the enabled flag.
func (s *IMChannelStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return s.Update(ctx, id, map[string]any{"enabled": enabled})
}

// TokenFor returns the persisted token for an instance, if any.
func (s *IMChannelStore) TokenFor(ctx context.Context, id string) (IMChannelToken, bool, error) {
	var row IMChannelToken
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return IMChannelToken{}, false, nil
		}
		return IMChannelToken{}, false, err
	}
	return row, true, nil
}

// SaveToken upserts a token row by instance id.
func (s *IMChannelStore) SaveToken(ctx context.Context, id, channel, tokenJSON string) error {
	return s.db.WithContext(ctx).Save(&IMChannelToken{
		ID:        id,
		Channel:   channel,
		TokenJSON: tokenJSON,
		UpdatedAt: time.Now().UnixMilli(),
	}).Error
}
