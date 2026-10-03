package billing

import (
	"errors"
	"fmt"
	"math"

	"quicksend/internal/subscription"
)

// Prices must match the website (frontend/src/lib/plans.ts): the website
// shows them, the backend charges them. Amounts are in minor units.

type Period string

const (
	PeriodMonth Period = "month"
	PeriodYear  Period = "year"
)

type Currency string

const (
	RUB Currency = "RUB"
	USD Currency = "USD"
)

const yearlyDiscount = 0.2

// monthly price in major units
var monthly = map[subscription.Plan]map[Currency]float64{
	subscription.PlanStandard: {RUB: 990, USD: 12},
	subscription.PlanPremium:  {RUB: 1990, USD: 22},
}

var ErrUnknownPlan = errors.New("billing: unknown plan or period")

// Price returns the amount charged for the period in minor units (kopecks, cents)
func Price(plan subscription.Plan, period Period, cur Currency) (int64, error) {
	base, ok := monthly[plan][cur]
	if !ok || (period != PeriodMonth && period != PeriodYear) {
		return 0, ErrUnknownPlan
	}
	if period == PeriodMonth {
		return toMinor(base, cur), nil
	}
	// the website rounds the discounted monthly price (RUB to rubles, USD to cents) and multiplies by 12
	return toMinor(base*(1-yearlyDiscount), cur) * 12, nil
}

func toMinor(v float64, cur Currency) int64 {
	if cur == RUB {
		return int64(math.Round(v)) * 100
	}
	return int64(math.Round(v * 100))
}

// Days the subscription lasts
func (p Period) Days() int {
	if p == PeriodYear {
		return 365
	}
	return 30
}

// FormatAmount renders minor units as "990.00" (the format YooKassa expects)
func FormatAmount(minor int64) string {
	return fmt.Sprintf("%d.%02d", minor/100, minor%100)
}
