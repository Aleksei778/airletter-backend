package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quicksend/internal/app"
	"quicksend/internal/auth"
	"quicksend/internal/config"
	"quicksend/internal/db"
	"quicksend/internal/httpapi"

	"github.com/getsentry/sentry-go"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	a, err := app.New(cfg)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	defer a.Close()
	defer sentry.Flush(2 * time.Second)

	// only the API migrates, so API and worker don't race on schema changes
	if err := db.Migrate(a.DB, app.Models()...); err != nil {
		slog.Error("migrate", "err", err)
		return
	}

	authStore := auth.NewStore(a.Redis.Raw(), time.Duration(cfg.JWTRefreshExpDays)*24*time.Hour)
	authSvc := auth.NewService(cfg, a.Users, a.Tokens, a.Subscriptions, authStore)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpapi.NewRouter(a, authSvc),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("api server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("api shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("api shutdown", "err", err)
	}
}
