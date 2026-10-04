package subscription

import (
	"airletter/internal/user"
	"errors"
	"time"

	"gorm.io/gorm"
)

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) FindActive(userID uint) (*Subscription, error) {
	var sub Subscription

	err := r.db.
		Where("user_id = ? AND is_active = true AND end_at > ?", userID, time.Now().UTC()).
		Order("end_at DESC").
		First(&sub).
		Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	return &sub, err
}

func (r *Repository) HasUsedTrial(u *user.User) (bool, error) {
	var count int64

	err := r.db.Model(&Subscription{}).
		Where("user_id = ? AND plan = ?", u.ID, PlanTrial).
		Count(&count).Error

	return count > 0, err
}

func (r *Repository) Create(sub *Subscription) error {
	return r.db.Create(sub).Error
}

// Activate gives the user a paid plan inside the caller's transaction and
// returns the new subscription. Buying the plan that is still active extends
// it from its end date; any other active subscription (trial, another plan)
// is replaced from now on.
func Activate(tx *gorm.DB, userID uint, plan Plan, days int, now time.Time) (*Subscription, error) {
	var current Subscription
	err := tx.Where("user_id = ? AND is_active = true AND end_at > ?", userID, now).
		Order("end_at DESC").First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	start := now
	if err == nil && current.Plan == plan {
		start = current.EndAt
	}

	if err := tx.Model(&Subscription{}).
		Where("user_id = ? AND is_active = true", userID).
		Update("is_active", false).Error; err != nil {
		return nil, err
	}

	sub := &Subscription{
		UserID:    userID,
		Plan:      plan,
		IsActive:  true,
		StartedAt: now,
		EndAt:     start.AddDate(0, 0, days),
	}
	if err := tx.Omit("User").Create(sub).Error; err != nil {
		return nil, err
	}
	return sub, nil
}
