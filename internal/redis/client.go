package redis

import (
	"context"
	"errors"
	"fmt"
	"quicksend/internal/config"
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
