package billing

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var (
	ErrUnknownProvider = errors.New("billing: provider is not configured")
	ErrBadSignature    = errors.New("billing: invalid webhook signature")
)

type CheckoutRequest struct {
	PaymentID   uint
	Amount      int64 // minor units
	Currency    Currency
	Description string
	ReturnURL   string
	Email       string // prefills the provider's form, optional
	Locale      string // "ru" or "en"
}

type Checkout struct {
	ExternalID      string
	ConfirmationURL string
}

// Provider is a payment service the user is redirected to
type Provider interface {
	Name() string
	Currency() Currency
	CreateCheckout(ctx context.Context, req CheckoutRequest) (*Checkout, error)
	// Status asks the provider for the current state: webhook bodies are not trusted
	Status(ctx context.Context, externalID string) (Status, error)
}

var httpClient = &http.Client{Timeout: 20 * time.Second}
