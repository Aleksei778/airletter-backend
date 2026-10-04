package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"airletter/internal/campaign"
	"airletter/internal/config"
	"airletter/internal/queue"
	"airletter/internal/redis"
	"airletter/internal/subscription"

	"github.com/hibiken/asynq"
)

// recipients stuck in queued/sending longer than this are returned to pending
const staleAfter = time.Hour

// Dispatcher handles campaign:dispatch. On every run it:
//  1. starts scheduled campaigns whose time has come
//  2. per user, releases a small batch of pending recipients into the send
//     queue, spaced by SendInterval, never exceeding the plan's daily limit
//  3. marks campaigns without unsent recipients as completed
//
// Releasing per user in small batches keeps one big campaign from blocking
// other users and paces sending so Gmail does not flag the account.
type Dispatcher struct {
	campaigns *campaign.Repository
	subs      *subscription.Service
	redis     *redis.Client
	client    *asynq.Client
	cfg       *config.Config
}

func NewDispatcher(campaigns *campaign.Repository, subs *subscription.Service, rdb *redis.Client, client *asynq.Client, cfg *config.Config) *Dispatcher {
	return &Dispatcher{campaigns: campaigns, subs: subs, redis: rdb, client: client, cfg: cfg}
}

func (d *Dispatcher) Handle(ctx context.Context, _ *asynq.Task) error {
	now := time.Now().UTC()

	if n, err := d.campaigns.ResetStale(ctx, now.Add(-staleAfter)); err != nil {
		return fmt.Errorf("dispatch: reset stale: %w", err)
	} else if n > 0 {
		slog.Warn("dispatch: returned stale recipients to pending", "count", n)
	}

	if _, err := d.campaigns.ActivateDue(ctx, now); err != nil {
		return fmt.Errorf("dispatch: activate due: %w", err)
	}

	users, err := d.campaigns.UsersWithSendingCampaigns(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: list users: %w", err)
	}

	for _, userID := range users {
		if err := d.dispatchUser(ctx, userID); err != nil {
			// one user's problem must not stop others
			slog.Error("dispatch: user", "err", err, "user_id", userID)
		}
	}

	if _, err := d.campaigns.CompleteFinished(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("dispatch: complete finished: %w", err)
	}

	return nil
}

func (d *Dispatcher) dispatchUser(ctx context.Context, userID uint) error {
	sub, err := d.subs.Active(userID)
	if err != nil {
		return err
	}
	if sub == nil {
		return d.campaigns.PauseUser(ctx, userID, campaign.PauseNoSubscription)
	}

	sentToday, err := d.redis.DailySentCount(ctx, userID)
	if err != nil {
		return fmt.Errorf("daily count: %w", err)
	}
	inFlight, err := d.campaigns.InFlight(ctx, userID)
	if err != nil {
		return fmt.Errorf("in flight: %w", err)
	}

	// Remaining pending recipients simply wait for tomorrow once the limit is hit
	budget := min(
		sub.Plan.DailyLimit()-sentToday-int(inFlight),
		d.perRun()-int(inFlight),
	)
	if budget <= 0 {
		return nil
	}

	ids, err := d.campaigns.ClaimPending(ctx, userID, budget)
	if err != nil {
		return fmt.Errorf("claim: %w", err)
	}

	var delay time.Duration
	for _, id := range ids {
		task, err := queue.NewSendEmailTask(id, d.cfg.SendMaxRetries, delay)
		if err != nil {
			return err
		}

		_, err = d.client.EnqueueContext(ctx, task)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
			// the recipient stays "queued" and is reset by ResetStale later
			return fmt.Errorf("enqueue recipient %d: %w", id, err)
		}

		delay += d.interval()
	}

	return nil
}

// perRun is how many emails of one user fit into one dispatch period
func (d *Dispatcher) perRun() int {
	return max(1, d.cfg.DispatchIntervalSeconds/max(1, d.cfg.SendIntervalSeconds))
}

func (d *Dispatcher) interval() time.Duration {
	jitter := time.Duration(0)
	if d.cfg.SendJitterSeconds > 0 {
		jitter = time.Duration(rand.Int64N(int64(d.cfg.SendJitterSeconds) * int64(time.Second)))
	}
	return time.Duration(d.cfg.SendIntervalSeconds)*time.Second + jitter
}
