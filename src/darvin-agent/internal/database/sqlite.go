package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var globalDB *gorm.DB

type Config struct {
	DSN string
}

func Init(cfg *Config) error {
	db, err := gorm.Open(sqlite.Open(cfg.DSN), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return err
	}

	globalDB = db
	return nil
}

func Get() *gorm.DB {
	if globalDB == nil {
		panic("database not initialized, call Init first")
	}
	return globalDB
}

func AutoMigrate(dst ...interface{}) error {
	return Get().AutoMigrate(dst...)
}
