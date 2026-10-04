package subscription

import (
	"log/slog"
	"net/http"
	"time"

	"airletter/internal/redis"

	"github.com/gin-gonic/gin"
)

type currentResponse struct {
	Plan       *Plan      `json:"plan"`
	EndAt      *time.Time `json:"end_at"`
	DailyLimit int        `json:"daily_limit"`
	SentToday  int        `json:"sent_today"`
	// paid plans that can be bought now, see CanBuy
	Purchasable []Plan `json:"purchasable"`
}

// RegisterRoutes mounts subscription endpoints; auth must be applied to r
func RegisterRoutes(r *gin.RouterGroup, svc *Service, rdb *redis.Client, userID func(*gin.Context) uint) {
	r.GET("/subscription/current", func(c *gin.Context) {
		uid := userID(c)

		sub, err := svc.Active(uid)
		if err != nil {
			slog.Error("subscription: current", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}

		sent, err := rdb.DailySentCount(c.Request.Context(), uid)
		if err != nil {
			slog.Error("subscription: daily count", "err", err)
		}

		resp := currentResponse{SentToday: sent, Purchasable: []Plan{}}
		for _, p := range PaidPlans {
			if CanBuy(sub, p, time.Now().UTC()) {
				resp.Purchasable = append(resp.Purchasable, p)
			}
		}
		if sub != nil {
			resp.Plan = &sub.Plan
			resp.EndAt = &sub.EndAt
			resp.DailyLimit = sub.Plan.DailyLimit()
		}
		c.JSON(http.StatusOK, resp)
	})
}

// RequirePaid lets only users with an active paid plan through
func RequirePaid(svc *Service, userID func(*gin.Context) uint) gin.HandlerFunc {
	return func(c *gin.Context) {
		sub, err := svc.Active(userID(c))
		if err != nil {
			slog.Error("subscription: require paid", "err", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if sub == nil || !sub.Plan.Paid() {
			c.AbortWithStatusJSON(http.StatusPaymentRequired, gin.H{"error": "available on paid plans", "code": "paid_plan_required"})
			return
		}
		c.Next()
	}
}
