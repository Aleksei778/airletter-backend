package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"airletter/internal/config"
	"airletter/internal/subscription"
	"airletter/internal/user"

	"gorm.io/gorm"
)

var (
	ErrNotFound = errors.New("billing: payment not found")
	// ErrPlanActive: the plan (or a higher one) is already active, see subscription.CanBuy
	ErrPlanActive = errors.New("billing: plan is already active")
)

// PaidHook runs after a payment activated a plan
type PaidHook func(ctx context.Context, userID uint) error

type Service struct {
	db        *gorm.DB
	cfg       *config.Config
	subs      *subscription.Service
	providers map[string]Provider
	onPaid    []PaidHook
}

// NewService enables the providers whose keys are configured
func NewService(db *gorm.DB, cfg *config.Config, subs *subscription.Service) *Service {
	s := &Service{db: db, cfg: cfg, subs: subs, providers: map[string]Provider{}}
	if cfg.YookassaShopID != "" && cfg.YookassaSecretKey != "" {
		s.addProvider(NewYooKassa(cfg.YookassaShopID, cfg.YookassaSecretKey, cfg.YookassaAPIURL))
	}
	if cfg.StripeSecretKey != "" {
		s.addProvider(NewStripe(cfg.StripeSecretKey, cfg.StripeWebhookSecret, cfg.StripeAPIURL))
	}
	return s
}

func (s *Service) addProvider(p Provider) { s.providers[p.Name()] = p }

func (s *Service) OnPaid(h PaidHook) { s.onPaid = append(s.onPaid, h) }

// Providers lists the enabled providers in display order
func (s *Service) Providers() []string {
	var names []string
	for _, n := range []string{"yookassa", "stripe"} {
		if _, ok := s.providers[n]; ok {
			names = append(names, n)
		}
	}
	return names
}

type CreateInput struct {
	Plan     subscription.Plan
	Period   Period
	Provider string
	Locale   string
}

// Create registers a pending payment and opens the provider's checkout
func (s *Service) Create(ctx context.Context, u *user.User, in CreateInput) (*Payment, string, error) {
	provider, ok := s.providers[in.Provider]
	if !ok {
		return nil, "", ErrUnknownProvider
	}
	amount, err := Price(in.Plan, in.Period, provider.Currency())
	if err != nil {
		return nil, "", err
	}
	current, err := s.subs.Active(u.ID)
	if err != nil {
		return nil, "", err
	}
	if !subscription.CanBuy(current, in.Plan, time.Now().UTC()) {
		return nil, "", ErrPlanActive
	}
	locale := "ru"
	if in.Locale == "en" {
		locale = "en"
	}

	p := &Payment{
		UserID:      u.ID,
		Provider:    provider.Name(),
		Plan:        string(in.Plan),
		Period:      in.Period,
		Amount:      FormatAmount(amount),
		Currency:    provider.Currency(),
		Status:      StatusPending,
		Description: describe(in.Plan, in.Period, locale),
	}
	if err := s.db.WithContext(ctx).Create(p).Error; err != nil {
		return nil, "", fmt.Errorf("billing: create payment: %w", err)
	}

	checkout, err := provider.CreateCheckout(ctx, CheckoutRequest{
		PaymentID:   p.ID,
		Amount:      amount,
		Currency:    provider.Currency(),
		Description: p.Description,
		ReturnURL:   fmt.Sprintf("%s/%s/dashboard/payment?id=%d", s.cfg.FrontendURL, locale, p.ID),
		Email:       u.Email,
		Locale:      locale,
	})
	if err != nil {
		s.db.WithContext(ctx).Model(p).Update("status", StatusCanceled)
		return nil, "", fmt.Errorf("billing: checkout: %w", err)
	}

	if err := s.db.WithContext(ctx).Model(p).Update("external_payment_id", checkout.ExternalID).Error; err != nil {
		return nil, "", fmt.Errorf("billing: save external id: %w", err)
	}
	return p, checkout.ConfirmationURL, nil
}

// Get returns the user's payment; a pending one is checked with the provider,
// so payments settle even when a webhook is late or not configured
func (s *Service) Get(ctx context.Context, userID, id uint) (*Payment, error) {
	var p Payment
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if p.Status == StatusPending && p.ExternalPaymentID != nil {
		if err := s.sync(ctx, &p); err != nil {
			slog.Error("billing: sync payment", "err", err, "payment_id", p.ID)
		}
	}
	return &p, nil
}

// SyncExternal is called from webhooks: the body only tells which payment
// to re-check, its status is always read from the provider
func (s *Service) SyncExternal(ctx context.Context, provider, externalID string) error {
	var p Payment
	err := s.db.WithContext(ctx).Where("provider = ? AND external_payment_id = ?", provider, externalID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if p.Status != StatusPending {
		return nil
	}
	return s.sync(ctx, &p)
}

func (s *Service) sync(ctx context.Context, p *Payment) error {
	provider, ok := s.providers[p.Provider]
	if !ok {
		return ErrUnknownProvider
	}
	status, err := provider.Status(ctx, *p.ExternalPaymentID)
	if err != nil {
		return err
	}

	switch status {
	case StatusSucceeded:
		return s.complete(ctx, p)
	case StatusCanceled:
		p.Status = StatusCanceled
		return s.db.WithContext(ctx).Model(&Payment{}).
			Where("id = ? AND status = ?", p.ID, StatusPending).
			Update("status", StatusCanceled).Error
	}
	return nil
}

// complete marks the payment paid and activates the plan exactly once,
// however many webhooks or status checks arrive
func (s *Service) complete(ctx context.Context, p *Payment) error {
	now := time.Now().UTC()
	activated := false

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Payment{}).
			Where("id = ? AND status = ?", p.ID, StatusPending).
			Updates(map[string]any{"status": StatusSucceeded, "paid_at": now})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error // already settled by a concurrent webhook
		}

		sub, err := subscription.Activate(tx, p.UserID, subscription.Plan(p.Plan), p.Period.Days(), now)
		if err != nil {
			return fmt.Errorf("activate plan: %w", err)
		}
		activated = true
		return tx.Model(&Payment{}).Where("id = ?", p.ID).Update("subscription_id", sub.ID).Error
	})
	if err != nil {
		return fmt.Errorf("billing: complete payment %d: %w", p.ID, err)
	}

	p.Status = StatusSucceeded
	p.PaidAt = &now
	if activated {
		slog.Info("billing: plan activated", "user_id", p.UserID, "plan", p.Plan, "period", p.Period, "payment_id", p.ID)
		for _, h := range s.onPaid {
			if err := h(ctx, p.UserID); err != nil {
				slog.Error("billing: paid hook", "err", err, "user_id", p.UserID)
			}
		}
	}
	return nil
}

func describe(plan subscription.Plan, period Period, locale string) string {
	if locale == "ru" {
		name := map[subscription.Plan]string{subscription.PlanStandard: "Стандарт", subscription.PlanPremium: "Премиум"}[plan]
		term := "1 месяц"
		if period == PeriodYear {
			term = "1 год"
		}
		return fmt.Sprintf("Доступ к сервису Airletter, тариф «%s», %s", name, term)
	}
	term := "1 month"
	if period == PeriodYear {
		term = "1 year"
	}
	return fmt.Sprintf("Airletter %s plan, %s", strings.ToUpper(string(plan[:1]))+string(plan[1:]), term)
}
