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

	_ "time/tzdata" // timezones for scheduling; the alpine image has no zoneinfo

	"quicksend/internal/app"
	"quicksend/internal/auth"
	"quicksend/internal/campaign"
	"quicksend/internal/config"
	"quicksend/internal/db"
	"quicksend/internal/google/sheets"
	"quicksend/internal/httpapi"
	"quicksend/internal/queue"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
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

	// only the API migrates; the worker expects the schema to be in place
	if cfg.MigrateOnStart {
		if err := db.MigrateUp(cfg); err != nil {
			slog.Error("migrate", "err", err)
			return
		}
	}

	authStore := auth.NewStore(a.Redis.Raw(), time.Duration(cfg.JWTRefreshExpDays)*24*time.Hour)
	authSvc := auth.NewService(cfg, a.Users, a.Tokens, a.Subscriptions, authStore)
	// after re-login with a fresh Google grant, continue campaigns paused for it
	authSvc.OnGrant(func(ctx context.Context, userID uint) error {
		return a.Campaigns.ResumeUser(ctx, userID, campaign.PauseReauthRequired)
	})

	trigger := queue.NewTrigger(a.Queue, time.Duration(cfg.DispatchIntervalSeconds)*time.Second)
	campaignSvc := campaign.NewService(a.Campaigns, a.Subscriptions, cfg, trigger)
	sheetsSvc := sheets.NewService(a.Tokens)

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(a, authSvc,
			func(r *gin.RouterGroup) { campaign.RegisterRoutes(r, campaignSvc, a.Users, auth.CurrentUserID) },
			func(r *gin.RouterGroup) { sheets.RegisterRoutes(r, sheetsSvc, auth.CurrentUserID) },
		),
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
