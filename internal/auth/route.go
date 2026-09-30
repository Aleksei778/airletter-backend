package auth

import (
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"quicksend/internal/config"
	usermod "quicksend/internal/user"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

const (
	accessCookie  = "access_jwt_token"
	refreshCookie = "refresh_jwt_token"
	// refresh cookie is only sent to auth endpoints
	refreshCookiePath = "/api/auth"
	// readable by the website's JS to show "Dashboard" instead of "Sign in";
	// carries no secret
	sessionMarkerCookie = "al_session"

	extensionCompletePath = "/api/auth/extension/complete"
)

// where the Google consent was started from
const (
	flowDashboard = "dashboard"
	flowExtension = "extension"
)

type handler struct {
	svc *Service
	cfg *config.Config
}

// RegisterRoutes mounts public auth endpoints and the Google integration.
// protected must already require an access token.
func RegisterRoutes(r *gin.RouterGroup, protected *gin.RouterGroup, svc *Service, cfg *config.Config) {
	h := &handler{svc: svc, cfg: cfg}

	g := r.Group("/auth")
	g.POST("/register", h.register)
	g.POST("/login", h.login)
	g.POST("/exchange", h.exchange)
	g.POST("/refresh", h.refresh)
	g.POST("/logout", h.logout)
	g.GET("/extension/start", h.extensionStart)
	g.GET("/extension/complete", h.extensionComplete)
	g.GET("/google/callback", h.googleCallback)

	// browser navigation: authenticated by the access cookie inside the handler
	r.GET("/integrations/google/connect", h.googleConnect)
	protected.GET("/integrations/google", h.googleStatus)
	protected.DELETE("/integrations/google", h.googleDisconnect)
}

// ---- email / phone + password ----

type credentials struct {
	Login    string `json:"login" binding:"required"`
	Password string `json:"password" binding:"required"`
	Name     string `json:"name"`
}

func (h *handler) register(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "login and password are required", "code": "invalid_request"})
		return
	}

	u, err := h.svc.Register(c.Request.Context(), RegisterInput{Login: req.Login, Password: req.Password, Name: req.Name})
	switch {
	case errors.Is(err, ErrInvalidLogin):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "code": "invalid_login"})
		return
	case errors.Is(err, ErrWeakPassword):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "code": "weak_password"})
		return
	case errors.Is(err, usermod.ErrLoginTaken):
		c.JSON(http.StatusConflict, gin.H{"error": "this email or phone is already registered", "code": "login_taken"})
		return
	case err != nil:
		slog.Error("auth: register", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.signIn(c, u, http.StatusCreated)
}

func (h *handler) login(c *gin.Context) {
	var req credentials
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "login and password are required", "code": "invalid_request"})
		return
	}

	u, err := h.svc.Login(c.Request.Context(), req.Login, req.Password, c.ClientIP())
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "wrong login or password", "code": "invalid_credentials"})
		return
	case errors.Is(err, ErrTooManyAttempts):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many attempts, try again in 15 minutes", "code": "too_many_attempts"})
		return
	case err != nil:
		slog.Error("auth: login", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.signIn(c, u, http.StatusOK)
}

func (h *handler) signIn(c *gin.Context, u *usermod.User, status int) {
	pair, err := h.svc.IssuePair(c.Request.Context(), u)
	if err != nil {
		slog.Error("auth: issue tokens", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.setCookies(c, pair)
	c.JSON(status, gin.H{"user": gin.H{"id": u.ID, "email": u.Email, "phone": u.Phone}})
}

// ---- extension sign-in (runs inside chrome.identity.launchWebAuthFlow) ----

// extensionStart remembers the extension's state and sends the user to the
// website's sign-in page, which comes back to extensionComplete.
func (h *handler) extensionStart(c *gin.Context) {
	extState := c.Query("state")
	if extState == "" || h.cfg.ExtensionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state is required"})
		return
	}

	session := sessions.Default(c)
	session.Set("ext_state", extState)
	if err := session.Save(); err != nil {
		slog.Error("auth: save session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session error"})
		return
	}

	c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/login?next="+url.QueryEscape(extensionCompletePath))
}

// extensionComplete: signed in on the website; make sure Gmail is connected,
// then hand a one-time code back to the extension.
func (h *handler) extensionComplete(c *gin.Context) {
	extState, _ := sessions.Default(c).Get("ext_state").(string)
	if extState == "" {
		c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/dashboard")
		return
	}

	userID, ok := h.userFromCookie(c)
	if !ok {
		c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/login?next="+url.QueryEscape(extensionCompletePath))
		return
	}

	connected, err := h.svc.HasGoogle(userID)
	if err != nil {
		slog.Error("auth: check google", "err", err)
		h.extensionFail(c, extState, "server_error")
		return
	}
	if !connected {
		h.startGoogle(c, userID, flowExtension, false)
		return
	}

	h.extensionSucceed(c, userID, extState)
}

func (h *handler) extensionSucceed(c *gin.Context, userID uint, extState string) {
	session := sessions.Default(c)
	session.Delete("ext_state")
	_ = session.Save()

	code, err := h.svc.NewLoginCode(c.Request.Context(), userID)
	if err != nil {
		slog.Error("auth: new login code", "err", err)
		h.extensionFail(c, extState, "server_error")
		return
	}
	c.Redirect(http.StatusFound, h.extensionCallback(url.Values{"code": {code}, "state": {extState}}))
}

func (h *handler) extensionFail(c *gin.Context, extState, code string) {
	c.Redirect(http.StatusFound, h.extensionCallback(url.Values{"error": {code}, "state": {extState}}))
}

func (h *handler) extensionCallback(q url.Values) string {
	return "https://" + h.cfg.ExtensionID + ".chromiumapp.org/callback?" + q.Encode()
}

// ---- Google integration ----

func (h *handler) googleConnect(c *gin.Context) {
	userID, ok := h.userFromCookie(c)
	if !ok {
		c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/login?next="+url.QueryEscape("/dashboard"))
		return
	}
	h.startGoogle(c, userID, flowDashboard, c.Query("consent") == "1")
}

// startGoogle binds the consent to the signed-in user through the session
// (signed cookie) and redirects to Google
func (h *handler) startGoogle(c *gin.Context, userID uint, flow string, forceConsent bool) {
	state := rand.Text()

	session := sessions.Default(c)
	session.Set("oauth_state", state)
	session.Set("oauth_user", strconv.FormatUint(uint64(userID), 10))
	session.Set("oauth_flow", flow)
	if err := session.Save(); err != nil {
		slog.Error("auth: save session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session error"})
		return
	}

	c.Redirect(http.StatusFound, h.svc.GoogleAuthURL(state, forceConsent))
}

func (h *handler) googleCallback(c *gin.Context) {
	session := sessions.Default(c)
	state, _ := session.Get("oauth_state").(string)
	flow, _ := session.Get("oauth_flow").(string)
	extState, _ := session.Get("ext_state").(string)
	userID64, _ := strconv.ParseUint(stringOr(session.Get("oauth_user"), "0"), 10, 64)
	userID := uint(userID64)

	// state is single-use
	session.Delete("oauth_state")
	_ = session.Save()

	fail := func(code string) {
		if flow == flowExtension && extState != "" {
			h.extensionFail(c, extState, code)
			return
		}
		c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/dashboard?gmail_error="+url.QueryEscape(code))
	}

	if state == "" || state != c.Query("state") || userID == 0 {
		fail("invalid_state")
		return
	}
	if c.Query("error") != "" {
		fail("access_denied")
		return
	}

	err := h.svc.ConnectGoogle(c.Request.Context(), userID, c.Query("code"))
	switch {
	case errors.Is(err, ErrConsentRequired):
		h.startGoogle(c, userID, flow, true)
		return
	case errors.Is(err, ErrMissingScopes):
		fail("missing_scopes")
		return
	case err != nil:
		slog.Error("auth: connect google", "err", err)
		fail("server_error")
		return
	}

	if flow == flowExtension && extState != "" {
		h.extensionSucceed(c, userID, extState)
		return
	}
	c.Redirect(http.StatusFound, h.cfg.FrontendURL+"/dashboard?gmail=connected")
}

func (h *handler) googleStatus(c *gin.Context) {
	acc, err := h.svc.tokenSvc.GoogleAccount(CurrentUserID(c))
	if err != nil {
		slog.Error("auth: google status", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connected": acc.Connected, "email": acc.Email})
}

func (h *handler) googleDisconnect(c *gin.Context) {
	if err := h.svc.tokenSvc.Disconnect(c.Request.Context(), CurrentUserID(c)); err != nil {
		slog.Error("auth: google disconnect", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"connected": false})
}

// ---- tokens ----

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
		h.clearCookies(c)
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

// ---- helpers ----

func (h *handler) userFromCookie(c *gin.Context) (uint, bool) {
	raw, err := c.Cookie(accessCookie)
	if err != nil {
		return 0, false
	}
	claims, err := h.svc.VerifyAccessToken(bearer(raw))
	if err != nil {
		return 0, false
	}
	return claims.UserID, true
}

func (h *handler) setCookies(c *gin.Context, pair *TokenPair) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookie, pair.AccessToken, int(pair.AccessTTL.Seconds()), "/", "", h.cfg.CookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(refreshCookie, pair.RefreshToken, int(pair.RefreshTTL.Seconds()), refreshCookiePath, "", h.cfg.CookieSecure, true)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(sessionMarkerCookie, "1", int(pair.RefreshTTL.Seconds()), "/", "", h.cfg.CookieSecure, false)
}

func (h *handler) clearCookies(c *gin.Context) {
	for _, ck := range []struct {
		name, path string
		httpOnly   bool
	}{
		{accessCookie, "/", true},
		{refreshCookie, refreshCookiePath, true},
		{sessionMarkerCookie, "/", false},
	} {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(ck.name, "", -1, ck.path, "", h.cfg.CookieSecure, ck.httpOnly)
	}
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
