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

	"airletter/internal/app"
	"airletter/internal/auth"
	"airletter/internal/billing"
	"airletter/internal/campaign"
	"airletter/internal/config"
	"airletter/internal/db"
	"airletter/internal/google/sheets"
	"airletter/internal/httpapi"
	"airletter/internal/queue"
	"airletter/internal/subscription"

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
	campaignSvc := campaign.NewService(a.Campaigns, a.Subscriptions, a.Tokens, cfg, trigger)
	sheetsSvc := sheets.NewService(a.Tokens)

	billingSvc := billing.NewService(a.DB, cfg, a.Subscriptions)
	slog.Info("payment providers", "enabled", billingSvc.Providers())
	// a paid plan continues campaigns that stopped when the previous one ended
	billingSvc.OnPaid(func(ctx context.Context, userID uint) error {
		return a.Campaigns.ResumeUser(ctx, userID, campaign.PauseNoSubscription)
	})

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(a, authSvc,
			func(_, r *gin.RouterGroup) { campaign.RegisterRoutes(r, campaignSvc, a.Users, auth.CurrentUserID) },
			func(_, r *gin.RouterGroup) {
				// trial users add recipients by hand; import is a paid feature
				paid := r.Group("", subscription.RequirePaid(a.Subscriptions, auth.CurrentUserID))
				sheets.RegisterRoutes(paid, sheetsSvc, auth.CurrentUserID)
			},
			func(pub, r *gin.RouterGroup) { billing.RegisterRoutes(pub, r, billingSvc, a.Users, auth.CurrentUserID) },
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
