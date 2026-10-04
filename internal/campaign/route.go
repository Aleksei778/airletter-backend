package campaign

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"airletter/internal/user"

	"github.com/gin-gonic/gin"
)

// base64 inflates attachments by ~4/3, plus room for the body
const maxRequestBytes = 32 << 20

type attachmentRequest struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimetype"`
	Content  string `json:"content"` // base64
	// set for images shown inside the HTML body as <img src="cid:...">
	ContentID string `json:"content_id"`
}

type createRequest struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	// "html" (default) or "text"
	Format      string              `json:"format"`
	Locale      string              `json:"locale"`
	Recipients  []string            `json:"recipients"`
	Attachments []attachmentRequest `json:"attachments"`
	// Files is the legacy name used by the extension
	Files []attachmentRequest `json:"files"`

	// Either scheduled_at (RFC 3339) or date+time+timezone from the extension
	ScheduledAt *time.Time `json:"scheduled_at"`
	Date        string     `json:"date"`
	Time        string     `json:"time"`
	Timezone    string     `json:"timezone"`
}

type campaignResponse struct {
	ID          uint       `json:"id"`
	Subject     string     `json:"subject"`
	Status      Status     `json:"status"`
	PauseReason string     `json:"pause_reason,omitempty"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Stats       Stats      `json:"stats"`
}

func toResponse(c WithStats) campaignResponse {
	return campaignResponse{
		ID:          c.ID,
		Subject:     c.Subject,
		Status:      c.Status,
		PauseReason: c.PauseReason,
		ScheduledAt: c.ScheduledAt,
		StartedAt:   c.StartedAt,
		FinishedAt:  c.FinishedAt,
		CreatedAt:   c.CreatedAt,
		Stats:       c.Stats,
	}
}

type handler struct {
	svc    *Service
	users  *user.Service
	userID func(*gin.Context) uint
}

// RegisterRoutes mounts campaign endpoints; auth must be applied to r
func RegisterRoutes(r *gin.RouterGroup, svc *Service, users *user.Service, userID func(*gin.Context) uint) {
	h := &handler{svc: svc, users: users, userID: userID}

	r.POST("/campaigns", h.create)
	r.GET("/campaigns", h.list)
	r.GET("/campaigns/:id", h.get)
	r.POST("/campaigns/:id/cancel", h.cancel)
	r.GET("/me", h.me)
}

func (h *handler) create(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBytes)

	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request is too large"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}

	in := CreateInput{Subject: req.Subject, Body: req.Body, Format: BodyFormat(req.Format), Locale: req.Locale, Recipients: req.Recipients}

	for _, a := range append(req.Attachments, req.Files...) {
		content, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "attachment " + a.Filename + " is not valid base64"})
			return
		}
		in.Attachments = append(in.Attachments, AttachmentInput{Filename: a.Filename, MimeType: a.MimeType, Content: content, ContentID: a.ContentID})
	}

	in.ScheduledAt = req.ScheduledAt
	if in.ScheduledAt == nil {
		t, err := ParseSchedule(req.Date, req.Time, req.Timezone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		in.ScheduledAt = t
	}

	u, ok := h.currentUser(c)
	if !ok {
		return
	}

	res, err := h.svc.Create(c.Request.Context(), u, in)
	var vErr *ValidationError
	switch {
	case errors.As(err, &vErr):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": vErr.Msg})
		return
	case errors.Is(err, ErrGmailNotConnected):
		// same code as a revoked grant: clients offer to (re)connect Gmail
		c.JSON(http.StatusConflict, gin.H{"error": "gmail is not connected", "code": "reauth_required"})
		return
	case errors.Is(err, ErrNoSubscription):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "no active subscription", "code": "no_subscription"})
		return
	case err != nil:
		slog.Error("campaign: create", "err", err, "user_id", u.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	stats := Stats{Total: int64(len(res.Campaign.Recipients)), Pending: int64(len(res.Campaign.Recipients))}
	c.JSON(http.StatusCreated, gin.H{
		"campaign": toResponse(WithStats{Campaign: *res.Campaign, Stats: stats}),
		"skipped":  nonNil(res.Skipped),
	})
}

func (h *handler) list(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit = min(max(limit, 1), 100)
	offset = max(offset, 0)

	items, err := h.svc.List(c.Request.Context(), h.userID(c), limit, offset)
	if err != nil {
		slog.Error("campaign: list", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	res := make([]campaignResponse, len(items))
	for i, item := range items {
		res[i] = toResponse(item)
	}
	c.JSON(http.StatusOK, gin.H{"campaigns": res})
}

func (h *handler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	item, err := h.svc.Get(c.Request.Context(), id, h.userID(c))
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign not found"})
		return
	}
	if err != nil {
		slog.Error("campaign: get", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, toResponse(*item))
}

func (h *handler) cancel(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}

	err := h.svc.Cancel(c.Request.Context(), id, h.userID(c))
	if errors.Is(err, ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "campaign not found or already finished"})
		return
	}
	if err != nil {
		slog.Error("campaign: cancel", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cancelled"})
}

// me returns the profile with campaign totals for the website dashboard
func (h *handler) me(c *gin.Context) {
	u, ok := h.currentUser(c)
	if !ok {
		return
	}

	totals, err := h.svc.Totals(c.Request.Context(), u.ID)
	if err != nil {
		slog.Error("campaign: totals", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"email":       u.Email,
		"first_name":  u.FirstName,
		"last_name":   u.LastName,
		"picture_url": u.PictureUrl,
		"totals":      totals,
	})
}

func (h *handler) currentUser(c *gin.Context) (*user.User, bool) {
	u, err := h.users.FindByID(h.userID(c))
	if err != nil {
		slog.Error("campaign: find user", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil, false
	}
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		return nil, false
	}
	return u, true
}

func parseID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return uint(id), true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
