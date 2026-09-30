package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/mail"

	"quicksend/internal/campaign"
	"quicksend/internal/google/gmail"
	"quicksend/internal/mailer"
	"quicksend/internal/queue"
	"quicksend/internal/redis"
	"quicksend/internal/token"
	"quicksend/internal/user"

	"github.com/hibiken/asynq"
)

// Sender handles email:send tasks: one task sends one email to one recipient
type Sender struct {
	campaigns *campaign.Repository
	users     *user.Service
	tokens    *token.Service
	gmail     *gmail.Service
	redis     *redis.Client
}

func NewSender(campaigns *campaign.Repository, users *user.Service, tokens *token.Service, gm *gmail.Service, rdb *redis.Client) *Sender {
	return &Sender{campaigns: campaigns, users: users, tokens: tokens, gmail: gm, redis: rdb}
}

func (s *Sender) Handle(ctx context.Context, t *asynq.Task) error {
	var p queue.SendEmailPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("sender: bad payload: %v: %w", err, asynq.SkipRetry)
	}

	// Guards against sending twice: only queued/sending recipients proceed
	ok, err := s.campaigns.StartSending(ctx, p.RecipientID)
	if err != nil {
		return fmt.Errorf("sender: start: %w", err)
	}
	if !ok {
		return nil
	}

	rec, err := s.campaigns.FindRecipient(ctx, p.RecipientID)
	if err != nil {
		return fmt.Errorf("sender: find recipient: %w", err)
	}

	c, err := s.campaigns.FindWithAttachments(ctx, rec.CampaignID)
	if err != nil {
		return fmt.Errorf("sender: find campaign: %w", err)
	}

	switch c.Status {
	case campaign.StatusCancelled:
		return s.campaigns.SetRecipientStatus(ctx, rec.ID, campaign.RecipientCancelled)
	case campaign.StatusPaused:
		return s.campaigns.SetRecipientStatus(ctx, rec.ID, campaign.RecipientPending)
	}

	u, err := s.users.FindByID(c.UserID)
	if err != nil || u == nil {
		return fmt.Errorf("sender: find user %d: %v", c.UserID, err)
	}

	// From must be the connected Gmail address: Gmail rewrites any other one
	acc, err := s.tokens.GoogleAccount(u.ID)
	if err != nil {
		return fmt.Errorf("sender: google account: %w", err)
	}
	from := acc.Email
	if from == "" {
		from = u.Email
	}

	raw, err := mailer.Build(buildMessage(c, from, rec.Email))
	if err != nil {
		_ = s.campaigns.MarkFailed(ctx, rec.ID, "build message: "+err.Error())
		return fmt.Errorf("sender: build message: %v: %w", err, asynq.SkipRetry)
	}

	msg, err := s.gmail.Send(ctx, u.ID, raw)
	if err == nil {
		if err := s.campaigns.MarkSent(ctx, rec.ID, msg.Id, msg.ThreadId); err != nil {
			// the email is already sent; don't retry, just log
			slog.Error("sender: mark sent", "err", err, "recipient_id", rec.ID)
		}
		if err := s.redis.IncrDailySentCount(ctx, u.ID); err != nil {
			slog.Error("sender: incr daily count", "err", err, "user_id", u.ID)
		}
		return nil
	}

	return s.handleSendError(ctx, err, u.ID, rec.ID)
}

func (s *Sender) handleSendError(ctx context.Context, sendErr error, userID, recipientID uint) error {
	switch classify(sendErr) {
	case errReauth:
		slog.Warn("sender: google access revoked, pausing campaigns", "user_id", userID, "err", sendErr)
		if err := s.tokens.Invalidate(userID); err != nil {
			slog.Error("sender: invalidate token", "err", err, "user_id", userID)
		}
		if err := s.campaigns.PauseUser(ctx, userID, campaign.PauseReauthRequired); err != nil {
			return fmt.Errorf("sender: pause user: %w", err)
		}
		return fmt.Errorf("sender: reauth required: %v: %w", sendErr, asynq.SkipRetry)

	case errPermanent:
		if err := s.campaigns.MarkFailed(ctx, recipientID, sendErr.Error()); err != nil {
			return fmt.Errorf("sender: mark failed: %w", err)
		}
		return fmt.Errorf("sender: permanent error: %v: %w", sendErr, asynq.SkipRetry)

	default:
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		if retried >= maxRetry {
			if err := s.campaigns.MarkFailed(ctx, recipientID, "retries exhausted: "+sendErr.Error()); err != nil {
				return fmt.Errorf("sender: mark failed: %w", err)
			}
		}
		return fmt.Errorf("sender: send: %w", sendErr)
	}
}

func buildMessage(c *campaign.Campaign, from, to string) mailer.Message {
	attachments := make([]mailer.Attachment, len(c.Attachments))
	for i, a := range c.Attachments {
		attachments[i] = mailer.Attachment{Filename: a.Filename, MimeType: a.MimeType, Content: a.Content}
	}

	return mailer.Message{
		From:        mail.Address{Name: c.SenderName, Address: from},
		To:          to,
		Subject:     c.Subject,
		HTML:        c.Body,
		Attachments: attachments,
	}
}
