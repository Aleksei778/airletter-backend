package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	"quicksend/internal/config"
	"quicksend/internal/subscription"
	"quicksend/internal/token"
	usermod "quicksend/internal/user"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	googleoauth "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

type Source string

const (
	SourceWebsite   Source = "website"
	SourceExtension Source = "extension"
)

const gmailSendScope = "https://www.googleapis.com/auth/gmail.send"

var (
	ErrMissingScopes = errors.New("auth: required google scopes were not granted")
	// ErrConsentRequired means Google did not return a refresh token and we have none stored
	ErrConsentRequired = errors.New("auth: offline consent required")
)

type userService interface {
	FindOrCreate(dto usermod.FindOrCreate) (*usermod.User, error)
	FindByID(id uint) (*usermod.User, error)
}

// GrantHook is called after the user granted (or re-granted) Google access
type GrantHook func(ctx context.Context, userID uint) error

type Service struct {
	cfg             *config.Config
	userSvc         userService
	tokenSvc        *token.Service
	subscriptionSvc *subscription.Service
	store           *Store
	onGrant         []GrantHook
}

func NewService(
	cfg *config.Config,
	userSvc userService,
	tokenSvc *token.Service,
	subscriptionSvc *subscription.Service,
	store *Store,
) *Service {
	return &Service{
		cfg:             cfg,
		userSvc:         userSvc,
		tokenSvc:        tokenSvc,
		subscriptionSvc: subscriptionSvc,
		store:           store,
	}
}

// OnGrant registers a hook run after a successful extension login
func (s *Service) OnGrant(h GrantHook) {
	s.onGrant = append(s.onGrant, h)
}

func (s *Service) oauthConfig(source Source) *oauth2.Config {
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

// AuthCodeURL builds the Google consent URL. forceConsent is needed to
// receive a refresh token when Google has already been granted access before.
func (s *Service) AuthCodeURL(source Source, state string, forceConsent bool) string {
	prompt := "select_account"
	if forceConsent {
		prompt = "consent"
	}

	opts := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("prompt", prompt)}
	if source == SourceExtension {
		opts = append(opts, oauth2.AccessTypeOffline)
	}

	return s.oauthConfig(source).AuthCodeURL(state, opts...)
}

// CompleteLogin exchanges the code, creates/updates the user and, for the
// extension, stores Google tokens and starts the trial.
func (s *Service) CompleteLogin(ctx context.Context, source Source, code string) (*usermod.User, error) {
	oauthToken, err := s.oauthConfig(source).Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("auth: exchange code: %w", err)
	}

	info, err := s.userInfo(ctx, oauthToken)
	if err != nil {
		return nil, fmt.Errorf("auth: user info: %w", err)
	}

	u, err := s.userSvc.FindOrCreate(usermod.FindOrCreate{
		Email:      strings.ToLower(info.Email),
		FirstName:  info.GivenName,
		LastName:   info.FamilyName,
		PictureUrl: info.Picture,
		OauthID:    info.Id,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: find or create user: %w", err)
	}

	if source != SourceExtension {
		return u, nil
	}

	// Users can uncheck individual scopes on the consent screen
	if !hasScope(oauthToken, gmailSendScope) {
		return nil, ErrMissingScopes
	}

	if oauthToken.RefreshToken == "" {
		has, err := s.tokenSvc.HasRefreshToken(u.ID)
		if err != nil {
			return nil, fmt.Errorf("auth: check refresh token: %w", err)
		}
		if !has {
			return nil, ErrConsentRequired
		}
	}

	if _, err := s.tokenSvc.Upsert(token.FindOrCreate{
		User:    u,
		Access:  oauthToken.AccessToken,
		Refresh: oauthToken.RefreshToken,
		Expiry:  oauthToken.Expiry,
	}); err != nil {
		return nil, fmt.Errorf("auth: save google token: %w", err)
	}

	if err := s.subscriptionSvc.CreateTrial(u); err != nil {
		return nil, err
	}

	for _, h := range s.onGrant {
		if err := h(ctx, u.ID); err != nil {
			return nil, fmt.Errorf("auth: grant hook: %w", err)
		}
	}

	return u, nil
}

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

func (s *Service) userInfo(ctx context.Context, t *oauth2.Token) (*googleoauth.Userinfo, error) {
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
