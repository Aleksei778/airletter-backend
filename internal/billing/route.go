package billing

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"airletter/internal/subscription"
	"airletter/internal/user"

	"github.com/gin-gonic/gin"
)

type handler struct {
	svc    *Service
	users  *user.Service
	userID func(*gin.Context) uint
}

// RegisterRoutes mounts payment endpoints: checkout and status under
// protected, provider list and webhooks under public
func RegisterRoutes(public, protected *gin.RouterGroup, svc *Service, users *user.Service, userID func(*gin.Context) uint) {
	h := &handler{svc: svc, users: users, userID: userID}

	public.GET("/billing/providers", h.providers)
	public.POST("/billing/webhooks/yookassa", h.yookassaWebhook)
	public.POST("/billing/webhooks/stripe", h.stripeWebhook)

	protected.POST("/payments", h.create)
	protected.GET("/payments/:id", h.get)
}

func (h *handler) providers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"providers": h.svc.Providers()})
}

type createRequest struct {
	Plan     string `json:"plan" binding:"required"`
	Period   string `json:"period" binding:"required"`
	Provider string `json:"provider" binding:"required"`
	Locale   string `json:"locale"`
}

func (h *handler) create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan, period and provider are required"})
		return
	}

	u, err := h.users.FindByID(h.userID(c))
	if err != nil || u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		return
	}

	p, url, err := h.svc.Create(c.Request.Context(), u, CreateInput{
		Plan:     subscription.Plan(req.Plan),
		Period:   Period(req.Period),
		Provider: req.Provider,
		Locale:   req.Locale,
	})
	switch {
	case errors.Is(err, ErrUnknownPlan), errors.Is(err, ErrUnknownProvider):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	case errors.Is(err, ErrPlanActive):
		c.JSON(http.StatusConflict, gin.H{"error": "this plan is already active", "code": "plan_active"})
		return
	case err != nil:
		slog.Error("billing: create", "err", err, "user_id", u.ID)
		c.JSON(http.StatusBadGateway, gin.H{"error": "payment provider is unavailable"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"payment_id":       strconv.FormatUint(uint64(p.ID), 10),
		"confirmation_url": url,
	})
}

func (h *handler) get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	p, err := h.svc.Get(c.Request.Context(), h.userID(c), uint(id))
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}
	if err != nil {
		slog.Error("billing: get", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": p.Status, "plan": p.Plan, "period": p.Period})
}

// yookassaWebhook: YooKassa does not sign notifications, so the body is only
// used to find the payment; its status is fetched from the YooKassa API.
func (h *handler) yookassaWebhook(c *gin.Context) {
	var n struct {
		Event  string `json:"event"`
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20)).Decode(&n); err != nil || n.Object.ID == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	h.settle(c, "yookassa", n.Object.ID)
}

func (h *handler) stripeWebhook(c *gin.Context) {
	stripe, ok := h.svc.providers["stripe"].(*Stripe)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}

	payload, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	sessionID, err := stripe.VerifyWebhook(payload, c.GetHeader("Stripe-Signature"), time.Now())
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if sessionID == "" {
		c.Status(http.StatusOK) // an event we do not handle
		return
	}
	h.settle(c, "stripe", sessionID)
}

func (h *handler) settle(c *gin.Context, provider, externalID string) {
	err := h.svc.SyncExternal(c.Request.Context(), provider, externalID)
	switch {
	case errors.Is(err, ErrNotFound):
		c.Status(http.StatusOK) // not ours: acknowledge so the provider stops retrying
	case err != nil:
		slog.Error("billing: webhook", "err", err, "provider", provider, "external_id", externalID)
		c.Status(http.StatusInternalServerError) // the provider will retry
	default:
		c.Status(http.StatusOK)
	}
}
