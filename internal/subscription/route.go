package subscription

import (
	"log/slog"
	"net/http"
	"time"

	"quicksend/internal/redis"

	"github.com/gin-gonic/gin"
)

type currentResponse struct {
	Plan       *Plan      `json:"plan"`
	EndAt      *time.Time `json:"end_at"`
	DailyLimit int        `json:"daily_limit"`
	SentToday  int        `json:"sent_today"`
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

		resp := currentResponse{SentToday: sent}
		if sub != nil {
			resp.Plan = &sub.Plan
			resp.EndAt = &sub.EndAt
			resp.DailyLimit = sub.Plan.DailyLimit()
		}
		c.JSON(http.StatusOK, resp)
	})
}
