package subscription

import (
	"time"

	"quicksend/internal/user"

	"gorm.io/gorm"
)

type Plan string

const (
	PlanTrial    Plan = "trial"
	PlanStandard Plan = "standard"
	PlanPremium  Plan = "premium"
)

func (p Plan) DailyLimit() int {
	switch p {
	case PlanTrial:
		return 30
	case PlanStandard:
		return 500
	case PlanPremium:
		return 2000
	default:
		return 0
	}
}

// Paid reports whether the plan was bought; paid plans unlock Google Sheets
// import and send without the Airletter footer
func (p Plan) Paid() bool { return p == PlanStandard || p == PlanPremium }

func (p Plan) rank() int {
	switch p {
	case PlanStandard:
		return 1
	case PlanPremium:
		return 2
	default:
		return 0
	}
}

// RenewWindow is how long before the end a paid plan can be paid for again
const RenewWindow = 7 * 24 * time.Hour

// PaidPlans in upgrade order
var PaidPlans = []Plan{PlanStandard, PlanPremium}

// CanBuy tells whether plan may be bought on top of the current subscription
// (nil when there is none): a higher plan any time, the same plan only close
// to its end, a lower plan never while a paid one is active.
func CanBuy(current *Subscription, plan Plan, now time.Time) bool {
	if !plan.Paid() {
		return false
	}
	if current == nil || !current.Plan.Paid() {
		return true
	}
	switch {
	case plan.rank() > current.Plan.rank():
		return true
	case plan == current.Plan:
		return current.EndAt.Sub(now) <= RenewWindow
	default:
		return false
	}
}

func (p Plan) DaysCount() int {
	switch p {
	case PlanTrial:
		return 10
	default:
		now := time.Now()
		return time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
}

type Subscription struct {
	gorm.Model

	Plan                  Plan      `gorm:"not null"`
	IsActive              bool      `gorm:"default:true"`
	AutoRenew             bool      `gorm:"default:false"`
	StartedAt             time.Time `gorm:"not null"`
	EndAt                 time.Time `gorm:"not null"`
	CanceledAt            *time.Time
	FailedPaymentAttempts int       `gorm:"default:0"`
	UserID                uint      `gorm:"not null"`
	User                  user.User `gorm:"foreignKey:UserID"`
}
