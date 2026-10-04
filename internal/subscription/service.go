package subscription

import (
	"airletter/internal/user"
	"fmt"
	"time"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateTrial(u *user.User) error {
	used, err := s.repo.HasUsedTrial(u)
	if err != nil {
		return fmt.Errorf("subscription: check trial: %w", err)
	}
	if used {
		return nil
	}

	now := time.Now().UTC()
	sub := &Subscription{
		UserID:    u.ID,
		Plan:      PlanTrial,
		IsActive:  true,
		StartedAt: now,
		EndAt:     now.AddDate(0, 0, PlanTrial.DaysCount()),
	}

	return s.repo.Create(sub)
}

// Active returns the current active subscription or nil if there is none
func (s *Service) Active(userID uint) (*Subscription, error) {
	sub, err := s.repo.FindActive(userID)
	if err != nil {
		return nil, fmt.Errorf("subscription: find active: %w", err)
	}
	return sub, nil
}
