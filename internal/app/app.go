// Package app wires dependencies shared by the API and the worker processes.
package app

import (
	"fmt"
	"log/slog"
	"os"

	"airletter/internal/campaign"
	"airletter/internal/config"
	"airletter/internal/crypto"
	"airletter/internal/db"
	"airletter/internal/logger"
	"airletter/internal/redis"
	"airletter/internal/subscription"
	"airletter/internal/token"
	"airletter/internal/user"

	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type App struct {
	Cfg   *config.Config
	DB    *gorm.DB
	Redis *redis.Client
	Queue *asynq.Client

	Users         *user.Service
	Tokens        *token.Service
	Subscriptions *subscription.Service
	Campaigns     *campaign.Repository
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

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("app: parse redis url: %w", err)
	}

	return &App{
		Cfg:           cfg,
		DB:            gdb,
		Redis:         rdb,
		Queue:         asynq.NewClient(redisOpt),
		Users:         user.NewService(gdb, user.NewRepository(gdb)),
		Tokens:        token.NewService(gdb, token.NewRepository(gdb), cfg),
		Subscriptions: subscription.NewService(subscription.NewRepository(gdb)),
		Campaigns:     campaign.NewRepository(gdb),
	}, nil
}

func (a *App) Close() {
	if err := a.Queue.Close(); err != nil {
		slog.Error("app: close queue client", "err", err)
	}
	if err := a.Redis.Close(); err != nil {
		slog.Error("app: close redis", "err", err)
	}
	if sqlDB, err := a.DB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			slog.Error("app: close db", "err", err)
		}
	}
}
