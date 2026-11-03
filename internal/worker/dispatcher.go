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
//  2. per user, releases into the send queue the pending recipients whose
//     turn comes before the next run, never exceeding the plan's daily limit
//  3. marks campaigns without unsent recipients as completed
//
// Emails of one user are spaced by the pause of the campaign each belongs
// to (at least SendInterval) plus jitter, so Gmail does not flag the account.
// The moment the next email may go out is kept in Redis between runs, so a
// pause longer than the dispatch period is kept too. Releasing only what fits
// before the next run keeps one big campaign from blocking other users.
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
	budget := sub.Plan.DailyLimit() - sentToday - int(inFlight)
	if budget <= 0 {
		return nil
	}

	now := time.Now().UTC()
	slot, err := d.redis.NextSendAt(ctx, userID)
	if err != nil {
		return fmt.Errorf("next send: %w", err)
	}
	slot = later(slot, now)
	horizon := now.Add(time.Duration(d.cfg.DispatchIntervalSeconds) * time.Second)
	if !slot.Before(horizon) {
		return nil
	}

	start := slot
	defer func() {
		if !slot.Equal(start) {
			if err := d.redis.SetNextSendAt(ctx, userID, slot); err != nil {
				slog.Error("dispatch: save next send", "err", err, "user_id", userID)
			}
		}
	}()

	for ; budget > 0 && slot.Before(horizon); budget-- {
		next, ok, err := d.campaigns.ClaimNext(ctx, userID)
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
		if !ok {
			return nil
		}

		task, err := queue.NewSendEmailTask(next.ID, d.cfg.SendMaxRetries, slot.Sub(now))
		if err != nil {
			return err
		}

		_, err = d.client.EnqueueContext(ctx, task)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) {
			// the recipient stays "queued" and is reset by ResetStale later
			return fmt.Errorf("enqueue recipient %d: %w", next.ID, err)
		}

		slot = slot.Add(d.interval(next.IntervalSeconds))
	}

	return nil
}

// interval is the pause after an email of a campaign that asked for
// campaignSeconds (0: the default), never shorter than SendInterval
func (d *Dispatcher) interval(campaignSeconds int) time.Duration {
	seconds := max(campaignSeconds, d.cfg.SendIntervalSeconds)
	jitter := time.Duration(0)
	if d.cfg.SendJitterSeconds > 0 {
		jitter = time.Duration(rand.Int64N(int64(d.cfg.SendJitterSeconds) * int64(time.Second)))
	}
	return time.Duration(seconds)*time.Second + jitter
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
