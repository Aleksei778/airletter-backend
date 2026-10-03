package billing

import (
	"time"

	"gorm.io/gorm"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusCanceled  Status = "canceled"
)

type Payment struct {
	gorm.Model
	UserID            uint   `gorm:"not null"`
	SubscriptionID    *uint  // set when the payment succeeds
	Provider          string `gorm:"not null"`
	ExternalPaymentID *string
	Plan              string `gorm:"not null"`
	Period            Period `gorm:"not null"`
	Amount            string `gorm:"type:numeric(10,2)"` // major units, e.g. "990.00"
	Currency          Currency
	Status            Status `gorm:"not null"`
	PaymentMethod     string
	Description       string
	PaidAt            *time.Time
}
