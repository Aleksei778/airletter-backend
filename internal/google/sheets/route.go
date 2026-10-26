package sheets

import (
	"errors"
	"log/slog"
	"net/http"

	"airletter/internal/token"

	"github.com/gin-gonic/gin"
)

type parseRequest struct {
	// Spreadsheet ID or full URL
	SpreadsheetID string `json:"spreadsheet_id" binding:"required"`
	Range         string `json:"range" binding:"required"`
}

type pickerTokenRequest struct {
	Code string `json:"code" binding:"required"`
}

// RegisterRoutes mounts sheet endpoints. Auth must be applied to r; pub is
// for the Picker page, which is authenticated by the one-time code.
func RegisterRoutes(pub, r *gin.RouterGroup, svc *Service, userID func(*gin.Context) uint) {
	r.POST("/sheets/parse", func(c *gin.Context) {
		var req parseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "spreadsheet_id and range are required"})
			return
		}

		emails, err := svc.ParseEmails(c.Request.Context(), userID(c), SpreadsheetID(req.SpreadsheetID), req.Range)

		switch {
		case err == nil:
			c.JSON(http.StatusOK, gin.H{"emails": emails})
		case errors.Is(err, ErrNoEmails):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no emails found in the first column of the range"})
		case token.IsReauthRequired(err):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "google access required", "code": "reauth_required"})
		case errors.Is(err, ErrFileAccessRequired):
			// the client opens Google Picker and retries
			c.JSON(http.StatusForbidden, gin.H{"error": "pick the spreadsheet in Google Drive first", "code": "file_access_required"})
		case errors.Is(err, ErrInvalidRange):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sheet name or range"})
		default:
			slog.Error("sheets: parse", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
	})

	// one-time code for the website's Picker page
	r.POST("/sheets/picker", func(c *gin.Context) {
		code, err := svc.NewPickerCode(c.Request.Context(), userID(c))
		if errors.Is(err, ErrPickerNotConfigured) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "spreadsheet import is temporarily unavailable"})
			return
		}
		if err != nil {
			slog.Error("sheets: picker code", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": code})
	})

	pub.POST("/sheets/picker/token", func(c *gin.Context) {
		var req pickerTokenRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
			return
		}

		s, err := svc.PickerSession(c.Request.Context(), req.Code)
		switch {
		case err == nil:
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusOK, gin.H{"access_token": s.AccessToken, "api_key": s.APIKey, "app_id": s.AppID})
		case errors.Is(err, ErrInvalidPickerCode):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired code", "code": "invalid_code"})
		case token.IsReauthRequired(err):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "google access required", "code": "reauth_required"})
		default:
			slog.Error("sheets: picker token", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
	})
}
