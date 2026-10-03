package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"quicksend/internal/config"
	"quicksend/internal/subscription"
	"quicksend/internal/token"
	usermod "quicksend/internal/user"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleoauth "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

type Source string

type JwtClaims struct {
	UserID  uint   `json:"user_id"`
	Email   string `json:"email"`
	OauthID string `json:"oauth_id"`
	Type    string `json:"type"`
	jwt.RegisteredClaims
}

type Service struct {
	cfg             *config.Config
	userSvc         *usermod.Service
	tokenSvc        *token.Service
	subscriptionSvc *subscription.Service
}

type JwtTokenPair struct {
	AccessToken  string
	RefreshToken string
}

const (
	SourceWebsite   Source = "website"
	SourceExtension Source = "extension"
)

func NewService(
	cfg *config.Config,
	userSvc *usermod.Service,
	tokenSvc *token.Service,
	subscriptionSvc *subscription.Service,
) *Service {
	return &Service{
		cfg:             cfg,
		userSvc:         userSvc,
		tokenSvc:        tokenSvc,
		subscriptionSvc: subscriptionSvc,
	}
}

func (s *Service) Login(c *gin.Context) {
	source := Source(c.DefaultQuery("source", "website"))
	lang := c.DefaultQuery("lang", "en")

	oauthConfig := s.createOauthConfig(source)
	state := rand.Text()

	session := sessions.Default(c)
	session.Set("source", source)
	session.Set("lang", lang)
	session.Set("state", state)
	err := session.Save()
	if err != nil {
		slog.Error("failed to save session", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session error"})
		return
	}

	url := oauthConfig.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)

	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (s *Service) Callback(c *gin.Context) {
	session := sessions.Default(c)

	state, _ := session.Get("state").(string)
	if state == "" || state != c.Query("state") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid oauth state"})
		return
	}

	source := Source(session.Get("source").(string))
	lang, _ := session.Get("lang").(string)

	if lang == "" {
		lang = "en"
	}

	oauthCfg := s.createOauthConfig(source)

	oauthToken, err := oauthCfg.Exchange(context.Background(), c.Query("code"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no google token data"})
		return
	}

	userInfo, err := s.getUserInfo(oauthToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "could not get user info"})
		return
	}

	u, err := s.userSvc.FindOrCreate(usermod.FindOrCreate{
		Email:      userInfo.Email,
		FirstName:  userInfo.GivenName,
		LastName:   userInfo.FamilyName,
		PictureUrl: userInfo.Picture,
		OauthID:    userInfo.Id,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if source == SourceExtension {
		_, err = s.tokenSvc.Upsert(token.FindOrCreate{
			User:    u,
			Access:  oauthToken.AccessToken,
			Refresh: oauthToken.RefreshToken,
			Expiry:  oauthToken.Expiry,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if err := s.subscriptionSvc.CreateTrial(u); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	accessToken, err := s.CreateAccessToken(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	refreshToken, err := s.CreateRefreshToken(u)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.redirect(c, accessToken, refreshToken, source, lang)
}

func (s *Service) createOauthConfig(source Source) *oauth2.Config {
	scopes := s.cfg.WebsiteScopes
	if source == SourceExtension {
		scopes = s.cfg.ExtensionScopes
	}

	return &oauth2.Config{
		ClientID:     s.cfg.GoogleClientID,
		ClientSecret: s.cfg.GoogleClientSecret,
		RedirectURL:  s.cfg.GoogleRedirectURI(),
		Scopes:       scopes,
		Endpoint:     google.Endpoint,
	}
}

func (s *Service) getUserInfo(token *oauth2.Token) (*googleoauth.Userinfo, error) {
	httpClient := oauth2.NewClient(context.Background(),
		oauth2.StaticTokenSource(token),
	)

	svc, err := googleoauth.NewService(context.Background(),
		option.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, err
	}

	info, err := svc.Userinfo.Get().Do()
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (s *Service) redirect(
	c *gin.Context,
	accessToken, refreshToken string,
	source Source,
	lang string,
) {
	if source == SourceWebsite {
		c.SetCookie(
			"access_jwt_token",
			"Bearer "+accessToken,
			s.cfg.JWTAccessExpHours*3600,
			"/", "", true, true,
		)
		c.SetCookie(
			"refresh_jwt_token",
			"Bearer "+refreshToken,
			s.cfg.JWTRefreshExpDays*86400,
			"/", "", true, true,
		)
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("%s/%s/profile", s.cfg.FrontendURL, lang),
		)
	} else {
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("https://%s.chromiumapp.org/callback?access_jwt_token=%s&refresh_jwt_token=%s",
				s.cfg.ExtensionID, accessToken, refreshToken,
			),
		)
	}
}

func (s *Service) CreateAccessToken(u *usermod.User) (string, error) {
	return s.sign(u, "access", time.Duration(s.cfg.JWTAccessExpHours)*time.Hour, s.cfg.JWTAccessSecret)
}

func (s *Service) CreateRefreshToken(u *usermod.User) (string, error) {
	return s.sign(u, "refresh", time.Duration(s.cfg.JWTRefreshExpDays)*24*time.Hour, s.cfg.JWTRefreshSecret)
}

func (s *Service) VerifyAccessToken(tokenStr string) (*JwtClaims, error) {
	return s.verify(tokenStr, "access", s.cfg.JWTAccessSecret)
}

func (s *Service) VerifyRefreshToken(tokenStr string) (*JwtClaims, error) {
	return s.verify(tokenStr, "refresh", s.cfg.JWTRefreshSecret)
}

func (s *Service) RefreshToken(tokenStr string) (*JwtTokenPair, error) {
	claims, err := s.VerifyRefreshToken(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("jwt: could not verify token: %w", err)
	}

	user, err := s.userSvc.FindByID(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("jwt: could not find user: %w", err)
	}

	accessToken, err := s.CreateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("jwt: could not create access token: %w", err)
	}

	refreshToken, err := s.CreateRefreshToken(user)
	if err != nil {
		return nil, fmt.Errorf("jwt: could not create refresh token: %w", err)
	}

	return &JwtTokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *Service) sign(u *usermod.User, tokenType string, ttl time.Duration, secret string) (string, error) {
	claims := JwtClaims{
		UserID:  u.ID,
		Email:   u.Email,
		OauthID: u.OauthID,
		Type:    tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func (s *Service) verify(tokenStr string, expectedType string, secret string) (*JwtClaims, error) {
	claims := &JwtClaims{}

	jwtToken, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}

		return []byte(secret), nil
	})

	if err != nil {
		return nil, err
	}

	if !jwtToken.Valid {
		return nil, errors.New("invalid token")
	}

	if claims.Type != expectedType {
		return nil, errors.New("invalid token type")
	}

	return claims, nil
}
