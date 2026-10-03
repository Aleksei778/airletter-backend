package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"quicksend/internal/config"
	"quicksend/internal/token"
	usermod "quicksend/internal/user"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleoauth "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

const gmailSendScope = "https://www.googleapis.com/auth/gmail.send"

var (
	ErrMissingScopes = errors.New("auth: required google scopes were not granted")
	// ErrConsentRequired means Google did not return a refresh token and we have none stored
	ErrConsentRequired = errors.New("auth: offline consent required")
)

type userService interface {
	Create(dto usermod.Create) (*usermod.User, error)
	FindByID(id uint) (*usermod.User, error)
	FindByEmail(email string) (*usermod.User, error)
	FillProfile(id uint, firstName, lastName, picture string) error
}

type trialCreator interface {
	CreateTrial(u *usermod.User) error
}

// GrantHook is called after the user connected (or reconnected) Google
type GrantHook func(ctx context.Context, userID uint) error

type Service struct {
	cfg      *config.Config
	userSvc  userService
	tokenSvc *token.Service
	trials   trialCreator
	store    *Store
	onGrant  []GrantHook
}

func NewService(
	cfg *config.Config,
	userSvc userService,
	tokenSvc *token.Service,
	trials trialCreator,
	store *Store,
) *Service {
	return &Service{cfg: cfg, userSvc: userSvc, tokenSvc: tokenSvc, trials: trials, store: store}
}

// OnGrant registers a hook run after Google access is granted
func (s *Service) OnGrant(h GrantHook) {
	s.onGrant = append(s.onGrant, h)
}

// ---- email + password ----

type RegisterInput struct {
	Email    string
	Password string
	Name     string
}

// Register creates an account and starts the free trial
func (s *Service) Register(ctx context.Context, in RegisterInput) (*usermod.User, error) {
	email, err := ParseEmail(in.Email)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	u, err := s.userSvc.Create(usermod.Create{Email: email, PasswordHash: hash, FirstName: in.Name})
	if err != nil {
		return nil, err
	}

	if err := s.trials.CreateTrial(u); err != nil {
		// the account exists; a missing trial is visible and fixable, a failed sign-up is not
		slog.Error("auth: create trial", "err", err, "user_id", u.ID)
	}
	return u, nil
}

// Login checks the password. Failed attempts are limited per email and per IP.
func (s *Service) Login(ctx context.Context, rawEmail, password, ip string) (*usermod.User, error) {
	email, err := ParseEmail(rawEmail)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	blocked, err := s.store.TooManyFailures(ctx, email, ip)
	if err != nil {
		return nil, fmt.Errorf("auth: check attempts: %w", err)
	}
	if blocked {
		return nil, ErrTooManyAttempts
	}

	u, err := s.userSvc.FindByEmail(email)
	if err != nil {
		return nil, fmt.Errorf("auth: find user: %w", err)
	}

	hash := ""
	if u != nil {
		hash = u.PasswordHash
	}
	if !checkPassword(hash, password) {
		s.store.RecordFailure(ctx, email, ip)
		return nil, ErrInvalidCredentials
	}

	s.store.ResetFailures(ctx, email)
	return u, nil
}

// ---- Google as an integration: permission to send via Gmail ----

func (s *Service) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     s.cfg.GoogleClientID,
		ClientSecret: s.cfg.GoogleClientSecret,
		RedirectURL:  s.cfg.GoogleRedirectURI(),
		Scopes:       s.cfg.ExtensionScopes,
		Endpoint:     google.Endpoint,
	}
}

// GoogleAuthURL builds the consent URL. forceConsent is needed to receive a
// refresh token when the account has already granted access before.
func (s *Service) GoogleAuthURL(state string, forceConsent bool) string {
	prompt := "select_account"
	if forceConsent {
		prompt = "consent"
	}
	return s.oauthConfig().AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", prompt))
}

// ConnectGoogle exchanges the code and stores the grant for the user
func (s *Service) ConnectGoogle(ctx context.Context, userID uint, code string) error {
	oauthToken, err := s.oauthConfig().Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("auth: exchange code: %w", err)
	}

	// users can uncheck individual scopes on the consent screen
	if !hasScope(oauthToken, gmailSendScope) {
		return ErrMissingScopes
	}

	if oauthToken.RefreshToken == "" {
		has, err := s.tokenSvc.HasRefreshToken(userID)
		if err != nil {
			return fmt.Errorf("auth: check refresh token: %w", err)
		}
		if !has {
			return ErrConsentRequired
		}
	}

	info, err := userInfo(ctx, oauthToken)
	if err != nil {
		return fmt.Errorf("auth: user info: %w", err)
	}

	if _, err := s.tokenSvc.Upsert(token.FindOrCreate{
		UserID:      userID,
		Access:      oauthToken.AccessToken,
		Refresh:     oauthToken.RefreshToken,
		Expiry:      oauthToken.Expiry,
		GoogleSub:   info.Id,
		GoogleEmail: strings.ToLower(info.Email),
	}); err != nil {
		return fmt.Errorf("auth: save google token: %w", err)
	}

	if err := s.userSvc.FillProfile(userID, info.GivenName, info.FamilyName, info.Picture); err != nil {
		slog.Error("auth: fill profile", "err", err, "user_id", userID)
	}

	for _, h := range s.onGrant {
		if err := h(ctx, userID); err != nil {
			return fmt.Errorf("auth: grant hook: %w", err)
		}
	}
	return nil
}

func (s *Service) HasGoogle(userID uint) (bool, error) {
	return s.tokenSvc.HasRefreshToken(userID)
}

// ---- one-time codes for the extension ----

// NewLoginCode creates a one-time code the extension exchanges for JWTs
func (s *Service) NewLoginCode(ctx context.Context, userID uint) (string, error) {
	code := rand.Text()
	if err := s.store.SaveLoginCode(ctx, code, userID); err != nil {
		return "", fmt.Errorf("auth: save login code: %w", err)
	}
	return code, nil
}

// ExchangeLoginCode turns a one-time code into a token pair
func (s *Service) ExchangeLoginCode(ctx context.Context, code string) (*TokenPair, error) {
	userID, ok, err := s.store.ConsumeLoginCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("auth: consume login code: %w", err)
	}
	if !ok {
		return nil, ErrInvalidToken
	}

	u, err := s.userSvc.FindByID(userID)
	if err != nil {
		return nil, fmt.Errorf("auth: find user: %w", err)
	}
	if u == nil {
		return nil, ErrInvalidToken
	}

	return s.IssuePair(ctx, u)
}

func userInfo(ctx context.Context, t *oauth2.Token) (*googleoauth.Userinfo, error) {
	svc, err := googleoauth.NewService(ctx, option.WithTokenSource(oauth2.StaticTokenSource(t)))
	if err != nil {
		return nil, err
	}
	return svc.Userinfo.Get().Context(ctx).Do()
}

func hasScope(t *oauth2.Token, scope string) bool {
	granted, _ := t.Extra("scope").(string)
	return slices.Contains(strings.Fields(granted), scope)
}
