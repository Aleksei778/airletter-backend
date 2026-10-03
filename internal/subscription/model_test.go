package subscription

import (
	"testing"
	"time"
)

func TestCanBuy(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sub := func(p Plan, left time.Duration) *Subscription { return &Subscription{Plan: p, EndAt: now.Add(left)} }
	month := 30 * 24 * time.Hour

	cases := []struct {
		name    string
		current *Subscription
		plan    Plan
		want    bool
	}{
		{"no plan", nil, PlanStandard, true},
		{"trial can buy standard", sub(PlanTrial, month), PlanStandard, true},
		{"trial cannot be bought", nil, PlanTrial, false},
		{"same plan early", sub(PlanStandard, month), PlanStandard, false},
		{"same plan near the end", sub(PlanStandard, 3*24*time.Hour), PlanStandard, true},
		{"upgrade", sub(PlanStandard, month), PlanPremium, true},
		{"downgrade", sub(PlanPremium, 2*24*time.Hour), PlanStandard, false},
	}
	for _, c := range cases {
		if got := CanBuy(c.current, c.plan, now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
