package db

import (
	"fmt"

	"quicksend/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Connect(cfg *config.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	return db, nil
}

// Migrate creates/updates tables for the given models.
// TODO: replace with versioned migrations before production.
func Migrate(db *gorm.DB, models ...any) error {
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("db: auto-migrate: %w", err)
	}
	return nil
}
