package auth

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"quicksend/internal/config"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const (
	accessCookie  = "access_jwt_token"
	refreshCookie = "refresh_jwt_token"
	// refresh cookie is only sent to auth endpoints
	refreshCookiePath = "/api/auth"
)

type handler struct {
	svc *Service
	cfg *config.Config
}

func RegisterRoutes(r *gin.RouterGroup, svc *Service, cfg *config.Config) {
	h := &handler{svc: svc, cfg: cfg}

	g := r.Group("/auth")
	g.GET("/google/login", h.login)
	g.GET("/google/callback", h.callback)
	g.POST("/exchange", h.exchange)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
}

// login redirects to Google. The extension passes its own `state`, which is
// returned to it unchanged so it can verify the response belongs to its request.
func (h *handler) login(c *gin.Context) {
	source := Source(c.DefaultQuery("source", string(SourceWebsite)))
	if source != SourceWebsite && source != SourceExtension {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid source"})
		return
	}

	extState := c.Query("state")
	if source == SourceExtension && extState == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state is required"})
		return
	}

	oauthState := rand.Text()

	session := sessions.Default(c)
	session.Set("oauth_state", oauthState)
	session.Set("source", string(source))
	session.Set("ext_state", extState)
	if err := session.Save(); err != nil {
		slog.Error("auth: save session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session error"})
		return
	}

	c.Redirect(http.StatusFound, h.svc.AuthCodeURL(source, oauthState, c.Query("consent") == "1"))
}

func (h *handler) callback(c *gin.Context) {
	session := sessions.Default(c)
	oauthState, _ := session.Get("oauth_state").(string)
	source := Source(stringOr(session.Get("source"), string(SourceWebsite)))
	extState, _ := session.Get("ext_state").(string)

	// state is single-use
	session.Delete("oauth_state")
	_ = session.Save()

	if oauthState == "" || oauthState != c.Query("state") {
		h.fail(c, source, extState, "invalid_state")
		return
	}
	if c.Query("error") != "" {
		h.fail(c, source, extState, "access_denied")
		return
	}

	u, err := h.svc.CompleteLogin(c.Request.Context(), source, c.Query("code"))
	switch {
	case errors.Is(err, ErrConsentRequired):
		q := url.Values{"source": {string(source)}, "state": {extState}, "consent": {"1"}}
		c.Redirect(http.StatusFound, "/api/auth/google/login?"+q.Encode())
		return
	case errors.Is(err, ErrMissingScopes):
		h.fail(c, source, extState, "missing_scopes")
		return
	case err != nil:
		slog.Error("auth: complete login", "err", err)
		h.fail(c, source, extState, "server_error")
		return
	}

	if source == SourceExtension {
		code, err := h.svc.NewLoginCode(c.Request.Context(), u.ID)
		if err != nil {
			slog.Error("auth: new login code", "err", err)
			h.fail(c, source, extState, "server_error")
			return
		}
		c.Redirect(http.StatusFound, h.extensionCallback(url.Values{"code": {code}, "state": {extState}}))
		return
	}

	pair, err := h.svc.IssuePair(c.Request.Context(), u)
	if err != nil {
		slog.Error("auth: issue tokens", "err", err)
		h.fail(c, source, extState, "server_error")
		return
	}
	h.setCookies(c, pair)
	c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/profile")
}

type exchangeRequest struct {
	Code string `json:"code" binding:"required"`
}

// exchange trades the one-time login code for JWTs (extension only)
func (h *handler) exchange(c *gin.Context) {
	var req exchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	pair, err := h.svc.ExchangeLoginCode(c.Request.Context(), req.Code)
	if errors.Is(err, ErrInvalidToken) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired code"})
		return
	}
	if err != nil {
		slog.Error("auth: exchange login code", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, pairJSON(pair))
}

// refresh accepts the refresh token from the Authorization header (extension)
// or from the cookie (website) and answers in the same form.
func (h *handler) refresh(c *gin.Context) {
	tokenStr, fromCookie := refreshTokenFrom(c)
	if tokenStr == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no refresh token"})
		return
	}

	pair, err := h.svc.Refresh(c.Request.Context(), tokenStr)
	if errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrTokenReused) {
		h.clearCookies(c)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}
	if err != nil {
		slog.Error("auth: refresh", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if fromCookie {
		h.setCookies(c, pair)
		c.Status(http.StatusNoContent)
		return
	}
	c.JSON(http.StatusOK, pairJSON(pair))
}

func (h *handler) logout(c *gin.Context) {
	if tokenStr, _ := refreshTokenFrom(c); tokenStr != "" {
		if err := h.svc.Logout(c.Request.Context(), tokenStr); err != nil {
			slog.Error("auth: logout", "err", err)
		}
	}
	h.clearCookies(c)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (h *handler) fail(c *gin.Context, source Source, extState, code string) {
	if source == SourceExtension {
		c.Redirect(http.StatusFound, h.extensionCallback(url.Values{"error": {code}, "state": {extState}}))
		return
	}
	c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/auth/login?error="+url.QueryEscape(code))
}

func (h *handler) extensionCallback(q url.Values) string {
	return "https://" + h.cfg.ExtensionID + ".chromiumapp.org/callback?" + q.Encode()
}

func (h *handler) setCookies(c *gin.Context, pair *TokenPair) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookie, pair.AccessToken, int(pair.AccessTTL.Seconds()), "/", "", h.cfg.CookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookie, pair.RefreshToken, int(pair.RefreshTTL.Seconds()), refreshCookiePath, "", h.cfg.CookieSecure, true)
}

func (h *handler) clearCookies(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookie, "", -1, "/", "", h.cfg.CookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookie, "", -1, refreshCookiePath, "", h.cfg.CookieSecure, true)
}

func pairJSON(pair *TokenPair) gin.H {
	return gin.H{
		"access_jwt_token":  pair.AccessToken,
		"refresh_jwt_token": pair.RefreshToken,
		"expires_in":        int(pair.AccessTTL.Seconds()),
	}
}

func refreshTokenFrom(c *gin.Context) (token string, fromCookie bool) {
	if t := bearer(c.GetHeader("Authorization")); t != "" {
		return t, false
	}
	if t, err := c.Cookie(refreshCookie); err == nil && t != "" {
		return bearer(t), true
	}
	return "", false
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
