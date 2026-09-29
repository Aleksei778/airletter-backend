package sheets

import (
	"errors"
	"log/slog"
	"net/http"

	"quicksend/internal/token"

	"github.com/gin-gonic/gin"
	"google.golang.org/api/googleapi"
)

type parseRequest struct {
	// Spreadsheet ID or full URL
	SpreadsheetID string `json:"spreadsheet_id" binding:"required"`
	Range         string `json:"range" binding:"required"`
}

// RegisterRoutes mounts sheet endpoints; auth must be applied to r
func RegisterRoutes(r *gin.RouterGroup, svc *Service, userID func(*gin.Context) uint) {
	r.POST("/sheets/parse", func(c *gin.Context) {
		var req parseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "spreadsheet_id and range are required"})
			return
		}

		emails, err := svc.ParseEmails(c.Request.Context(), userID(c), SpreadsheetID(req.SpreadsheetID), req.Range)

		var gErr *googleapi.Error
		switch {
		case err == nil:
			c.JSON(http.StatusOK, gin.H{"emails": emails})
		case errors.Is(err, ErrNoEmails):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "no emails found in the first column of the range"})
		case token.IsReauthRequired(err):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "google access required", "code": "reauth_required"})
		case errors.As(err, &gErr) && (gErr.Code == 400 || gErr.Code == 403 || gErr.Code == 404):
			c.JSON(http.StatusBadRequest, gin.H{"error": "spreadsheet not found, not accessible or invalid range"})
		default:
			slog.Error("sheets: parse", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
	})
}
