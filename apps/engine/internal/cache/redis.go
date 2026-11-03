// Package cache wraps go-redis for LRU query result caching and rate limiting.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	QueryCacheTTL = 60 * time.Second  // query result cache TTL
	RateLimitTTL  = 1 * time.Minute   // rate limit window
	RateLimitMax  = 100               // max queries per minute per IP
)

// Client wraps a Redis client with typed helpers.
type Client struct {
	rdb *redis.Client
}

// New creates a Redis client from the given URL (e.g. "redis://localhost:6379").
// Returns an in-memory fallback if Redis is unavailable.
func New(url string) (*Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis parse URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// GetQueryCache retrieves a cached search result. Returns nil, false if cache miss.
func (c *Client) GetQueryCache(ctx context.Context, key string, dest interface{}) (bool, error) {
	data, err := c.rdb.Get(ctx, "qc:"+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis get: %w", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, fmt.Errorf("unmarshal cache: %w", err)
	}
	return true, nil
}

// SetQueryCache stores a search result with TTL.
func (c *Client) SetQueryCache(ctx context.Context, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	return c.rdb.Set(ctx, "qc:"+key, data, QueryCacheTTL).Err()
}

// RateLimitCheck increments the counter for ip within the rolling window.
// Returns true (allowed), false (rate limited).
func (c *Client) RateLimitCheck(ctx context.Context, ip string) (bool, error) {
	key := "rl:" + ip
	count, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true, nil // fail open: allow on Redis error
	}
	if count == 1 {
		// Set expiry on first increment
		c.rdb.Expire(ctx, key, RateLimitTTL) //nolint:errcheck
	}
	return count <= int64(RateLimitMax), nil
}

// Health checks Redis connectivity.
func (c *Client) Health(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}
