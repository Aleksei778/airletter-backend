package campaign

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrNotFound = errors.New("campaign not found")

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create stores campaign with its recipients and attachments in one transaction
func (r *Repository) Create(ctx context.Context, c *Campaign) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		recipients, attachments := c.Recipients, c.Attachments
		c.Recipients, c.Attachments = nil, nil

		if err := tx.Create(c).Error; err != nil {
			return err
		}

		for i := range recipients {
			recipients[i].CampaignID = c.ID
			recipients[i].UserID = c.UserID
		}
		if err := tx.CreateInBatches(recipients, 500).Error; err != nil {
			return err
		}

		for i := range attachments {
			attachments[i].CampaignID = c.ID
		}
		if len(attachments) > 0 {
			if err := tx.Create(&attachments).Error; err != nil {
				return err
			}
		}

		c.Recipients, c.Attachments = recipients, attachments
		return nil
	})
}

func (r *Repository) ListByUser(ctx context.Context, userID uint, limit, offset int) ([]Campaign, error) {
	var campaigns []Campaign
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).Offset(offset).
		Find(&campaigns).Error
	return campaigns, err
}

func (r *Repository) FindForUser(ctx context.Context, id, userID uint) (*Campaign, error) {
	var c Campaign
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}

func (r *Repository) FindWithAttachments(ctx context.Context, id uint) (*Campaign, error) {
	var c Campaign
	err := r.db.WithContext(ctx).Preload("Attachments").First(&c, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}

// StatsByCampaign returns recipient counters for the given campaigns
func (r *Repository) StatsByCampaign(ctx context.Context, ids []uint) (map[uint]*Stats, error) {
	result := make(map[uint]*Stats, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	var rows []struct {
		CampaignID uint
		Status     RecipientStatus
		Count      int64
	}
	err := r.db.WithContext(ctx).Model(&Recipient{}).
		Select("campaign_id, status, count(*) AS count").
		Where("campaign_id IN ?", ids).
		Group("campaign_id, status").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		result[id] = &Stats{}
	}
	for _, row := range rows {
		s := result[row.CampaignID]
		s.Total += row.Count
		switch row.Status {
		case RecipientSent:
			s.Sent += row.Count
		case RecipientFailed:
			s.Failed += row.Count
		case RecipientPending, RecipientQueued, RecipientSending:
			s.Pending += row.Count
		}
	}
	return result, nil
}

type UserTotals struct {
	Campaigns  int64 `json:"campaigns"`
	Recipients int64 `json:"recipients"`
	Sent       int64 `json:"sent"`
}

func (r *Repository) TotalsByUser(ctx context.Context, userID uint) (*UserTotals, error) {
	var t UserTotals
	db := r.db.WithContext(ctx)

	if err := db.Model(&Campaign{}).Where("user_id = ?", userID).Count(&t.Campaigns).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&Recipient{}).Where("user_id = ?", userID).Count(&t.Recipients).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&Recipient{}).Where("user_id = ? AND status = ?", userID, RecipientSent).Count(&t.Sent).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// Cancel stops the campaign; recipients that were not sent yet are marked cancelled
func (r *Repository) Cancel(ctx context.Context, id, userID uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Campaign{}).
			Where("id = ? AND user_id = ? AND status IN ?", id, userID,
				[]Status{StatusScheduled, StatusSending, StatusPaused}).
			Updates(map[string]any{"status": StatusCancelled, "finished_at": time.Now().UTC()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}

		return tx.Model(&Recipient{}).
			Where("campaign_id = ? AND status IN ?", id, []RecipientStatus{RecipientPending, RecipientQueued}).
			Update("status", RecipientCancelled).Error
	})
}

// ---- dispatcher ----

// ActivateDue moves scheduled campaigns whose time has come to sending
func (r *Repository) ActivateDue(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&Campaign{}).
		Where("status = ? AND scheduled_at <= ?", StatusScheduled, now).
		Updates(map[string]any{"status": StatusSending, "started_at": now})
	return res.RowsAffected, res.Error
}

func (r *Repository) UsersWithSendingCampaigns(ctx context.Context) ([]uint, error) {
	var ids []uint
	err := r.db.WithContext(ctx).Model(&Campaign{}).
		Where("status = ?", StatusSending).
		Distinct().Pluck("user_id", &ids).Error
	return ids, err
}

// InFlight counts recipients of the user that are already in the queue
func (r *Repository) InFlight(ctx context.Context, userID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Recipient{}).
		Where("user_id = ? AND status IN ?", userID, []RecipientStatus{RecipientQueued, RecipientSending}).
		Count(&n).Error
	return n, err
}

// ClaimPending atomically marks up to limit pending recipients of the user as queued.
// SKIP LOCKED makes it safe to run from several worker instances at once.
func (r *Repository) ClaimPending(ctx context.Context, userID uint, limit int) ([]uint, error) {
	var ids []uint
	err := r.db.WithContext(ctx).Raw(`
		UPDATE recipients SET status = ?, updated_at = now()
		WHERE id IN (
			SELECT r.id FROM recipients r
			JOIN campaigns c ON c.id = r.campaign_id
			WHERE r.user_id = ? AND r.status = ? AND c.status = ? AND c.deleted_at IS NULL
			ORDER BY r.campaign_id, r.id
			LIMIT ?
			FOR UPDATE OF r SKIP LOCKED
		)
		RETURNING id`,
		RecipientQueued, userID, RecipientPending, StatusSending, limit,
	).Scan(&ids).Error
	return ids, err
}

// CompleteFinished marks sending campaigns without unsent recipients as completed
func (r *Repository) CompleteFinished(ctx context.Context, now time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE campaigns c SET status = ?, finished_at = ?, updated_at = ?
		WHERE c.status = ? AND c.deleted_at IS NULL AND NOT EXISTS (
			SELECT 1 FROM recipients r
			WHERE r.campaign_id = c.id AND r.status IN (?, ?, ?)
		)`,
		StatusCompleted, now, now, StatusSending,
		RecipientPending, RecipientQueued, RecipientSending,
	)
	return res.RowsAffected, res.Error
}

// PauseUser pauses all sending campaigns of the user and returns queued recipients to pending
func (r *Repository) PauseUser(ctx context.Context, userID uint, reason string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Model(&Campaign{}).
			Where("user_id = ? AND status = ?", userID, StatusSending).
			Updates(map[string]any{"status": StatusPaused, "pause_reason": reason}).Error
		if err != nil {
			return err
		}

		return tx.Model(&Recipient{}).
			Where("user_id = ? AND status IN ?", userID, []RecipientStatus{RecipientQueued, RecipientSending}).
			Update("status", RecipientPending).Error
	})
}

// ResumeUser resumes campaigns paused for the given reason
func (r *Repository) ResumeUser(ctx context.Context, userID uint, reason string) error {
	return r.db.WithContext(ctx).Model(&Campaign{}).
		Where("user_id = ? AND status = ? AND pause_reason = ?", userID, StatusPaused, reason).
		Updates(map[string]any{"status": StatusSending, "pause_reason": ""}).Error
}

// ---- sender ----

func (r *Repository) FindRecipient(ctx context.Context, id uint) (*Recipient, error) {
	var rec Recipient
	err := r.db.WithContext(ctx).First(&rec, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &rec, err
}

// StartSending moves recipient to sending. Returns false if the recipient
// is no longer expected to be sent (already sent, cancelled or returned to pending).
func (r *Repository) StartSending(ctx context.Context, id uint) (bool, error) {
	res := r.db.WithContext(ctx).Model(&Recipient{}).
		Where("id = ? AND status IN ?", id, []RecipientStatus{RecipientQueued, RecipientSending}).
		Updates(map[string]any{"status": RecipientSending, "attempts": gorm.Expr("attempts + 1")})
	return res.RowsAffected == 1, res.Error
}

func (r *Repository) MarkSent(ctx context.Context, id uint, messageID, threadID string) error {
	return r.db.WithContext(ctx).Model(&Recipient{}).Where("id = ?", id).
		Updates(map[string]any{
			"status":           RecipientSent,
			"sent_at":          time.Now().UTC(),
			"gmail_message_id": messageID,
			"gmail_thread_id":  threadID,
			"error":            "",
		}).Error
}

func (r *Repository) MarkFailed(ctx context.Context, id uint, reason string) error {
	return r.db.WithContext(ctx).Model(&Recipient{}).Where("id = ?", id).
		Updates(map[string]any{"status": RecipientFailed, "error": reason}).Error
}

// SetRecipientStatus is used when a queued email must not be sent anymore
// (campaign paused or cancelled after the task was queued)
func (r *Repository) SetRecipientStatus(ctx context.Context, id uint, status RecipientStatus) error {
	return r.db.WithContext(ctx).Model(&Recipient{}).Where("id = ?", id).
		Update("status", status).Error
}

// ResetStale returns recipients stuck in the queue (e.g. enqueue failed or the
// task was lost) back to pending so the dispatcher picks them up again
func (r *Repository) ResetStale(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Model(&Recipient{}).
		Where("status IN ? AND updated_at < ?", []RecipientStatus{RecipientQueued, RecipientSending}, before).
		Update("status", RecipientPending)
	return res.RowsAffected, res.Error
}
