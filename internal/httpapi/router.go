// Package httpapi builds the HTTP router: middleware and route groups.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"airletter/internal/app"
	"airletter/internal/auth"
	"airletter/internal/subscription"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// Routes lets feature packages mount their handlers: public is /api,
// protected is /api with an access token required
type Routes func(public, protected *gin.RouterGroup)

func NewRouter(a *app.App, authSvc *auth.Service, extra ...Routes) *gin.Engine {
	cfg := a.Cfg

	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	origins := []string{cfg.FrontendURL}
	if cfg.ExtensionID != "" {
		origins = append(origins, cfg.ExtensionOrigin())
	}
	r.Use(cors.New(cors.Config{
		AllowOrigins:           origins,
		AllowBrowserExtensions: true,
		AllowMethods:           []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:           []string{"Authorization", "Content-Type"},
		AllowCredentials:       true,
		MaxAge:                 12 * time.Hour,
	}))

	// short-lived session used only to carry OAuth and extension sign-in state
	// through redirects. SameSite=Lax is required: the Google callback is a
	// cross-site top-level navigation.
	store := cookie.NewStore([]byte(cfg.SessionSecret))
	store.Options(sessions.Options{
		Path:     "/api",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions("qs_oauth", store))

	r.GET("/health", func(c *gin.Context) {
		sqlDB, err := a.DB.DB()
		if err == nil {
			err = sqlDB.PingContext(c.Request.Context())
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	protected := api.Group("", auth.RequireAccessToken(authSvc))
	auth.RegisterRoutes(api, protected, authSvc, cfg)

	subscription.RegisterRoutes(protected, a.Subscriptions, a.Redis, auth.CurrentUserID)

	for _, register := range extra {
		register(api, protected)
	}

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("http request",
			"method", c.Request.Method,
			"path", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
}
