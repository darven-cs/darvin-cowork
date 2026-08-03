package database

import (
	"log"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var globalDB *gorm.DB

// Config holds the database connection settings. SessionsDSN must be
// an absolute path; main.go resolves the raw `database.sessions_dsn`
// value (which may be empty / relative) via
// config.ResolveSessionsDSN before constructing this struct.
type Config struct {
	SessionsDSN string
}

// Init opens the SQLite database at SessionsDSN and stores the handle
// in the package-level singleton. All SessionStore implementations must
// share this handle (see internal/agents/store.NewSQLiteStore) — opening
// the same DSN twice would race on SQLite's write lock.
//
// GORM's query log goes to stderr: stdout carries the Gateway port line
// (see internal/gateway.Server.Start) and must stay single-line.
func Init(cfg *Config) error {
	db, err := gorm.Open(sqlite.Open(cfg.SessionsDSN), &gorm.Config{
		Logger: gormlogger.New(
			log.New(os.Stderr, "[gorm] ", log.LstdFlags),
			gormlogger.Config{LogLevel: gormlogger.Info},
		),
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
