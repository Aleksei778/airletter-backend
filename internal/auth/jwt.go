package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	usermod "airletter/internal/user"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrTokenReused means a rotated refresh token was presented again, which
	// indicates it was stolen; all refresh tokens of the user get revoked.
	ErrTokenReused = errors.New("auth: refresh token reuse detected")
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

type JwtClaims struct {
	UserID uint   `json:"user_id"`
	Email  string `json:"email"`
	Type   string `json:"type"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
}

func (s *Service) accessTTL() time.Duration {
	return time.Duration(s.cfg.JWTAccessExpHours) * time.Hour
}

func (s *Service) refreshTTL() time.Duration {
	return time.Duration(s.cfg.JWTRefreshExpDays) * 24 * time.Hour
}

// IssuePair creates a new access/refresh pair and registers the refresh token
func (s *Service) IssuePair(ctx context.Context, u *usermod.User) (*TokenPair, error) {
	access, err := s.sign(u, tokenTypeAccess, s.accessTTL(), s.cfg.JWTAccessSecret, "")
	if err != nil {
		return nil, fmt.Errorf("auth: sign access: %w", err)
	}

	jti := rand.Text()
	refresh, err := s.sign(u, tokenTypeRefresh, s.refreshTTL(), s.cfg.JWTRefreshSecret, jti)
	if err != nil {
		return nil, fmt.Errorf("auth: sign refresh: %w", err)
	}

	if err := s.store.SaveRefresh(ctx, u.ID, jti); err != nil {
		return nil, fmt.Errorf("auth: save refresh: %w", err)
	}

	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		AccessTTL:    s.accessTTL(),
		RefreshTTL:   s.refreshTTL(),
	}, nil
}

// Refresh rotates the refresh token: the old one becomes invalid
func (s *Service) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.VerifyRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}

	ok, err := s.store.ConsumeRefresh(ctx, claims.UserID, claims.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: consume refresh: %w", err)
	}
	if !ok {
		if err := s.store.RevokeAll(ctx, claims.UserID); err != nil {
			return nil, fmt.Errorf("auth: revoke all: %w", err)
		}
		return nil, ErrTokenReused
	}

	u, err := s.userSvc.FindByID(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth: find user: %w", err)
	}
	if u == nil {
		return nil, ErrInvalidToken
	}

	return s.IssuePair(ctx, u)
}

// Logout revokes the given refresh token
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	claims, err := s.VerifyRefreshToken(refreshToken)
	if err != nil {
		return nil // nothing to revoke
	}
	_, err = s.store.ConsumeRefresh(ctx, claims.UserID, claims.ID)
	return err
}

func (s *Service) VerifyAccessToken(tokenStr string) (*JwtClaims, error) {
	return s.verify(tokenStr, tokenTypeAccess, s.cfg.JWTAccessSecret)
}

func (s *Service) VerifyRefreshToken(tokenStr string) (*JwtClaims, error) {
	return s.verify(tokenStr, tokenTypeRefresh, s.cfg.JWTRefreshSecret)
}

func (s *Service) sign(u *usermod.User, tokenType string, ttl time.Duration, secret, jti string) (string, error) {
	now := time.Now()
	claims := JwtClaims{
		UserID: u.ID,
		Email:  u.Email,
		Type:   tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func (s *Service) verify(tokenStr, expectedType, secret string) (*JwtClaims, error) {
	claims := &JwtClaims{}

	_, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, ErrInvalidToken
	}

	if claims.Type != expectedType {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
