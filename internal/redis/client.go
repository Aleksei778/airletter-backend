package redis

import (
	"airletter/internal/config"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	rdb *redis.Client
}

func NewClient(cfg *config.Config) (*Client, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url: %w", err)
	}

	rdb := redis.NewClient(opts)

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

func (c *Client) DailySentCount(ctx context.Context, userID uint) (int, error) {
	key := dailyKey(userID)
	val, err := c.rdb.Get(ctx, key).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}

	return val, err
}

func (c *Client) IncrDailySentCount(ctx context.Context, userID uint) error {
	key := dailyKey(userID)

	newVal, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return err
	}

	if newVal == 1 {
		// the key is per UTC day, keep it a bit longer than a day
		c.rdb.Expire(ctx, key, 48*time.Hour)
	}

	return nil
}

// NextSendAt is when the user's next email may go out; zero if not set
func (c *Client) NextSendAt(ctx context.Context, userID uint) (time.Time, error) {
	ms, err := c.rdb.Get(ctx, nextSendKey(userID)).Int64()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(ms).UTC(), nil
}

// SetNextSendAt stores when the user's next email may go out. The key lives
// a bit past that moment: once it expires, sending may start right away.
func (c *Client) SetNextSendAt(ctx context.Context, userID uint, at time.Time) error {
	ttl := time.Until(at) + time.Hour
	return c.rdb.Set(ctx, nextSendKey(userID), at.UnixMilli(), ttl).Err()
}

// Raw exposes the underlying client for other Redis-backed stores
func (c *Client) Raw() *redis.Client {
	return c.rdb
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func dailyKey(userID uint) string {
	return fmt.Sprintf("user:sent:%d:%s", userID, time.Now().UTC().Format("2006-01-02"))
}

func nextSendKey(userID uint) string {
	return fmt.Sprintf("user:next_send:%d", userID)
}
