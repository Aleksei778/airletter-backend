package db

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"

	"airletter/internal/config"
	"airletter/migrations"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

// MigrateUp applies pending migrations from the migrations package.
// It opens its own connection: the migrate driver closes the *sql.DB it gets.
// Concurrent runs (several API instances) are serialized by a Postgres advisory lock.
func MigrateUp(cfg *config.Config) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("db: migrations source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(cfg))
	if err != nil {
		return fmt.Errorf("db: init migrate: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	slog.Info("db: schema is up to date", "version", version, "dirty", dirty)
	return nil
}

func migrateURL(cfg *config.Config) string {
	u := url.URL{
		Scheme:   "pgx5",
		User:     url.UserPassword(cfg.DBUser, cfg.DBPass),
		Host:     cfg.DBHost + ":" + strconv.Itoa(cfg.DBPort),
		Path:     "/" + cfg.DBName,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}
