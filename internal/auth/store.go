package auth

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const loginCodeTTL = 60 * time.Second

// Store keeps server-side auth state in Redis:
//   - refresh token IDs (jti), so refresh tokens can be rotated and revoked
//   - one-time login codes handed to the extension instead of JWTs in the URL
type Store struct {
	rdb        *redis.Client
	refreshTTL time.Duration
}

func NewStore(rdb *redis.Client, refreshTTL time.Duration) *Store {
	return &Store{rdb: rdb, refreshTTL: refreshTTL}
}

func refreshKey(jti string) string { return "auth:refresh:" + jti }
func userRefreshKey(userID uint) string {
	return "auth:user_refresh:" + strconv.FormatUint(uint64(userID), 10)
}
func loginCodeKey(code string) string { return "auth:login_code:" + code }

func (s *Store) SaveRefresh(ctx context.Context, userID uint, jti string) error {
	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, refreshKey(jti), userID, s.refreshTTL)
	pipe.SAdd(ctx, userRefreshKey(userID), jti)
	pipe.Expire(ctx, userRefreshKey(userID), s.refreshTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ConsumeRefresh removes the refresh token ID. ok=false means the token
// was already used or revoked.
func (s *Store) ConsumeRefresh(ctx context.Context, userID uint, jti string) (bool, error) {
	_, err := s.rdb.GetDel(ctx, refreshKey(jti)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.rdb.SRem(ctx, userRefreshKey(userID), jti)
	return true, nil
}

// RevokeAll invalidates every refresh token of the user
func (s *Store) RevokeAll(ctx context.Context, userID uint) error {
	jtis, err := s.rdb.SMembers(ctx, userRefreshKey(userID)).Result()
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(jtis)+1)
	for _, jti := range jtis {
		keys = append(keys, refreshKey(jti))
	}
	keys = append(keys, userRefreshKey(userID))

	return s.rdb.Del(ctx, keys...).Err()
}

func (s *Store) SaveLoginCode(ctx context.Context, code string, userID uint) error {
	return s.rdb.Set(ctx, loginCodeKey(code), userID, loginCodeTTL).Err()
}

// ConsumeLoginCode returns the user ID for a one-time code and deletes it
func (s *Store) ConsumeLoginCode(ctx context.Context, code string) (uint, bool, error) {
	val, err := s.rdb.GetDel(ctx, loginCodeKey(code)).Uint64()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return uint(val), true, nil
}

const (
	attemptsWindow  = 15 * time.Minute
	maxLoginFails   = 10 // per login
	maxAddressFails = 50 // per IP, covers password spraying across logins
)

func failKey(kind, value string) string { return "auth:fail:" + kind + ":" + value }

// TooManyFailures reports whether sign-in is blocked for the login or the IP
func (s *Store) TooManyFailures(ctx context.Context, login, ip string) (bool, error) {
	counts, err := s.rdb.MGet(ctx, failKey("login", login), failKey("ip", ip)).Result()
	if err != nil {
		return false, err
	}
	return atoi(counts[0]) >= maxLoginFails || atoi(counts[1]) >= maxAddressFails, nil
}

func (s *Store) RecordFailure(ctx context.Context, login, ip string) {
	pipe := s.rdb.TxPipeline()
	for _, k := range []string{failKey("login", login), failKey("ip", ip)} {
		pipe.Incr(ctx, k)
		pipe.ExpireNX(ctx, k, attemptsWindow)
	}
	_, _ = pipe.Exec(ctx)
}

func (s *Store) ResetFailures(ctx context.Context, login string) {
	s.rdb.Del(ctx, failKey("login", login))
}

func atoi(v any) int {
	str, _ := v.(string)
	n, _ := strconv.Atoi(str)
	return n
}
