package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"quicksend/internal/config"
	usermod "quicksend/internal/user"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type fakeUsers struct{ u *usermod.User }

func (f fakeUsers) FindOrCreate(usermod.FindOrCreate) (*usermod.User, error) { return f.u, nil }
func (f fakeUsers) FindByID(uint) (*usermod.User, error)                     { return f.u, nil }

func newTestService(t *testing.T) (*Service, *usermod.User) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	cfg := &config.Config{
		JWTAccessSecret:   "access-secret",
		JWTRefreshSecret:  "refresh-secret",
		JWTAccessExpHours: 1,
		JWTRefreshExpDays: 30,
	}
	u := &usermod.User{Model: gorm.Model{ID: 42}, Email: "a@example.com"}
	svc := NewService(cfg, fakeUsers{u}, nil, nil, NewStore(rdb, 30*24*time.Hour))
	return svc, u
}

func TestRefreshRotatesToken(t *testing.T) {
	svc, u := newTestService(t)
	ctx := context.Background()

	first, err := svc.IssuePair(ctx, u)
	if err != nil {
		t.Fatal(err)
	}

	second, err := svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	claims, err := svc.VerifyAccessToken(second.AccessToken)
	if err != nil || claims.UserID != u.ID {
		t.Fatalf("access token: %v, %+v", err, claims)
	}
}

func TestRefreshReuseRevokesAll(t *testing.T) {
	svc, u := newTestService(t)
	ctx := context.Background()

	first, _ := svc.IssuePair(ctx, u)
	second, err := svc.Refresh(ctx, first.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Refresh(ctx, first.RefreshToken); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("reuse: got %v, want ErrTokenReused", err)
	}
	// the legitimate latest token is revoked too
	if _, err := svc.Refresh(ctx, second.RefreshToken); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("after revoke: got %v, want ErrTokenReused", err)
	}
}

func TestLogoutRevokesRefreshToken(t *testing.T) {
	svc, u := newTestService(t)
	ctx := context.Background()

	pair, _ := svc.IssuePair(ctx, u)
	if err := svc.Logout(ctx, pair.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refresh(ctx, pair.RefreshToken); err == nil {
		t.Fatal("refresh after logout succeeded")
	}
}

func TestTokenTypesAreNotInterchangeable(t *testing.T) {
	svc, u := newTestService(t)
	pair, _ := svc.IssuePair(context.Background(), u)

	if _, err := svc.VerifyAccessToken(pair.RefreshToken); err == nil {
		t.Error("refresh token accepted as access token")
	}
	if _, err := svc.VerifyRefreshToken(pair.AccessToken); err == nil {
		t.Error("access token accepted as refresh token")
	}
}

func TestLoginCodeIsSingleUse(t *testing.T) {
	svc, u := newTestService(t)
	ctx := context.Background()

	code, err := svc.NewLoginCode(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeLoginCode(ctx, code); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, err := svc.ExchangeLoginCode(ctx, code); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second exchange: got %v, want ErrInvalidToken", err)
	}
}
