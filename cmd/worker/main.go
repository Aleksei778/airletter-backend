package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quicksend/internal/app"
	"quicksend/internal/config"
	"quicksend/internal/google/gmail"
	"quicksend/internal/queue"
	"quicksend/internal/worker"

	"github.com/getsentry/sentry-go"
	"github.com/hibiken/asynq"
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

	redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL)
	if err != nil {
		slog.Error("parse redis url", "err", err)
		return
	}

	sender := worker.NewSender(a.Campaigns, a.Users, a.Tokens, gmail.NewService(a.Tokens), a.Redis)
	dispatcher := worker.NewDispatcher(a.Campaigns, a.Subscriptions, a.Redis, a.Queue, cfg)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeSendEmail, sender.Handle)
	mux.HandleFunc(queue.TypeDispatch, dispatcher.Handle)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.WorkerConcurrency,
		Queues: map[string]int{
			queue.QueueSend:    6,
			queue.QueueDefault: 3,
		},
		RetryDelayFunc: retryDelay,
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			slog.Error("task failed", "type", t.Type(), "err", err)
		}),
		ShutdownTimeout: 30 * time.Second,
	})

	interval := time.Duration(cfg.DispatchIntervalSeconds) * time.Second
	scheduler := asynq.NewScheduler(redisOpt, &asynq.SchedulerOpts{Location: time.UTC})
	if _, err := scheduler.Register(fmt.Sprintf("@every %ds", cfg.DispatchIntervalSeconds), queue.NewDispatchTask(interval)); err != nil {
		slog.Error("register dispatch schedule", "err", err)
		return
	}

	if err := srv.Start(mux); err != nil {
		slog.Error("start worker", "err", err)
		return
	}
	if err := scheduler.Start(); err != nil {
		slog.Error("start scheduler", "err", err)
		srv.Shutdown()
		return
	}
	slog.Info("worker started", "concurrency", cfg.WorkerConcurrency)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("worker shutting down")
	scheduler.Shutdown()
	srv.Shutdown()
}

// retryDelay is exponential backoff from 30s up to 30min with jitter
func retryDelay(n int, _ error, _ *asynq.Task) time.Duration {
	d := 30 * time.Second << min(n, 6)
	d = min(d, 30*time.Minute)
	return d/2 + time.Duration(rand.Int64N(int64(d/2)))
}
