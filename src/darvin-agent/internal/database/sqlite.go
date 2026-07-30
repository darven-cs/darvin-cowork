package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var globalDB *gorm.DB

// Config holds the database connection settings. SessionsDSN is the
// path passed to the SQLite driver (relative to the Go agent's cwd).
type Config struct {
	SessionsDSN string
}

// Init opens the SQLite database at SessionsDSN and stores the handle
// in the package-level singleton. All SessionStore implementations must
// share this handle (see internal/agent/store.NewSQLiteStore) — opening
// the same DSN twice would race on SQLite's write lock.
func Init(cfg *Config) error {
	db, err := gorm.Open(sqlite.Open(cfg.SessionsDSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return err
	}

	globalDB = db
	return nil
}

// Get returns the *gorm.DB opened by Init. Panics if Init has not run.
func Get() *gorm.DB {
	if globalDB == nil {
		panic("database not initialized, call Init first")
	}
	return globalDB
}

func AutoMigrate(dst ...interface{}) error {
	return Get().AutoMigrate(dst...)
}
