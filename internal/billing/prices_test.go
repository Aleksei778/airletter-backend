package billing

import (
	"testing"

	"airletter/internal/subscription"
)

// Same numbers as the website's pricing cards (frontend/src/lib/plans.ts)
func TestPricesMatchWebsite(t *testing.T) {
	tests := []struct {
		plan   subscription.Plan
		period Period
		cur    Currency
		want   int64
	}{
		{subscription.PlanStandard, PeriodMonth, RUB, 990_00},
		{subscription.PlanStandard, PeriodYear, RUB, 9504_00}, // 792 × 12
		{subscription.PlanPremium, PeriodMonth, RUB, 1990_00},
		{subscription.PlanPremium, PeriodYear, RUB, 19104_00}, // 1592 × 12
		{subscription.PlanStandard, PeriodMonth, USD, 12_00},
		{subscription.PlanStandard, PeriodYear, USD, 115_20}, // 9.60 × 12
		{subscription.PlanPremium, PeriodYear, USD, 211_20},  // 17.60 × 12
	}
	for _, tt := range tests {
		got, err := Price(tt.plan, tt.period, tt.cur)
		if err != nil || got != tt.want {
			t.Errorf("Price(%s, %s, %s) = %d, %v; want %d", tt.plan, tt.period, tt.cur, got, err, tt.want)
		}
	}

	if _, err := Price(subscription.PlanTrial, PeriodMonth, RUB); err == nil {
		t.Error("trial must not be purchasable")
	}
	if _, err := Price(subscription.PlanStandard, "week", RUB); err == nil {
		t.Error("unknown period accepted")
	}
}

func TestFormatAmount(t *testing.T) {
	if got := FormatAmount(9504_00); got != "9504.00" {
		t.Errorf("got %s", got)
	}
	if got := FormatAmount(115_20); got != "115.20" {
		t.Errorf("got %s", got)
	}
}
