// Package app wires dependencies shared by the API and the worker processes.
package app

import (
	"fmt"
	"log/slog"
	"os"

	"quicksend/internal/config"
	"quicksend/internal/crypto"
	"quicksend/internal/db"
	"quicksend/internal/logger"
	"quicksend/internal/models"
	"quicksend/internal/redis"
	"quicksend/internal/subscription"
	"quicksend/internal/token"
	"quicksend/internal/user"

	"gorm.io/gorm"
)

type App struct {
	Cfg   *config.Config
	DB    *gorm.DB
	Redis *redis.Client

	Users         *user.Service
	Tokens        *token.Service
	Subscriptions *subscription.Service
}

func New(cfg *config.Config) (*App, error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := logger.Init(cfg); err != nil {
		return nil, fmt.Errorf("app: init sentry: %w", err)
	}

	// must run before any DB access: token hooks encrypt with this key
	crypto.Init(cfg.EncryptionKey)

	gdb, err := db.Connect(cfg)
	if err != nil {
		return nil, err
	}

	rdb, err := redis.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &App{
		Cfg:           cfg,
		DB:            gdb,
		Redis:         rdb,
		Users:         user.NewService(gdb, user.NewRepository(gdb)),
		Tokens:        token.NewService(gdb, token.NewRepository(gdb), cfg),
		Subscriptions: subscription.NewService(subscription.NewRepository(gdb)),
	}, nil
}

// Models lists all tables managed by AutoMigrate
func Models() []any {
	return []any{
		&user.User{},
		&token.Token{},
		&subscription.Subscription{},
		&models.Payment{},
	}
}

func (a *App) Close() {
	if err := a.Redis.Close(); err != nil {
		slog.Error("app: close redis", "err", err)
	}
	if sqlDB, err := a.DB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			slog.Error("app: close db", "err", err)
		}
	}
}
