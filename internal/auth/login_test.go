package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"quicksend/internal/config"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestParseEmail(t *testing.T) {
	if got, err := ParseEmail(" Ivan@Example.COM "); err != nil || got != "ivan@example.com" {
		t.Errorf("ParseEmail = %q %v, want ivan@example.com", got, err)
	}

	for _, bad := range []string{"", "ivan", "ivan@localhost", "Ivan <ivan@example.com>", "+79161234567"} {
		if _, err := ParseEmail(bad); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("ParseEmail(%q): got %v, want ErrInvalidEmail", bad, err)
		}
	}
}

func TestHashPasswordLength(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short password: %v", err)
	}
	long := make([]byte, 73)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := HashPassword(string(long)); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("73-byte password: %v", err)
	}
}

func newPasswordService(t *testing.T) (*Service, *fakeTrials) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	trials := &fakeTrials{}
	cfg := &config.Config{JWTAccessSecret: "a", JWTRefreshSecret: "r", JWTAccessExpHours: 1, JWTRefreshExpDays: 30}
	return NewService(cfg, newFakeUsers(), nil, trials, NewStore(rdb, time.Hour)), trials
}

func TestRegisterAndLogin(t *testing.T) {
	svc, trials := newPasswordService(t)
	ctx := context.Background()

	u, err := svc.Register(ctx, RegisterInput{Email: "Ivan@Example.com", Password: "correct horse", Name: "Иван"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ivan@example.com" || u.PasswordHash == "correct horse" {
		t.Fatalf("user not normalized or password stored in plain text: %+v", u)
	}
	if len(trials.created) != 1 {
		t.Errorf("trial not created on sign-up")
	}

	if _, err := svc.Register(ctx, RegisterInput{Email: "ivan@example.com", Password: "another one"}); err == nil {
		t.Error("duplicate email registered")
	}

	got, err := svc.Login(ctx, "IVAN@example.com", "correct horse", "1.1.1.1")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login: %v %v", got, err)
	}
	if _, err := svc.Login(ctx, "ivan@example.com", "wrong password", "1.1.1.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := svc.Login(ctx, "nobody@example.com", "whatever1", "1.1.1.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("unknown login: %v", err)
	}
}

func TestLoginThrottling(t *testing.T) {
	svc, _ := newPasswordService(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, RegisterInput{Email: "a@example.com", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxLoginFails; i++ {
		_, _ = svc.Login(ctx, "a@example.com", "wrong", "2.2.2.2")
	}
	// even the right password is refused while blocked
	if _, err := svc.Login(ctx, "a@example.com", "correct horse", "3.3.3.3"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("got %v, want ErrTooManyAttempts", err)
	}
}
