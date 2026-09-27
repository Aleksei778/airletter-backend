package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const ctxUserID = "user_id"

// RequireAccessToken authenticates by the Authorization header (extension)
// or the access cookie (website).
func RequireAccessToken(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := bearer(c.GetHeader("Authorization"))
		if tokenStr == "" {
			if cookie, err := c.Cookie(accessCookie); err == nil {
				tokenStr = bearer(cookie)
			}
		}

		claims, err := svc.VerifyAccessToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid access token"})
			return
		}

		c.Set(ctxUserID, claims.UserID)
		c.Next()
	}
}

// CurrentUserID returns the authenticated user ID set by RequireAccessToken
func CurrentUserID(c *gin.Context) uint {
	return c.GetUint(ctxUserID)
}

func bearer(v string) string {
	return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
}
