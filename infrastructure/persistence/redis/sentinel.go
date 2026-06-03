// Package redis provides Redis Sentinel support for the idempotency repository.
//
// Usage:
//
//	import (
//	    goredis "github.com/redis/go-redis/v9"
//	    redisrepo "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/redis"
//	)
//
//	// Approach 1: Use the convenience function
//	client, err := redisrepo.NewSentinelClient(redisrepo.SentinelConfig{
//	    MasterName:   "mymaster",
//	    SentinelAddrs: []string{"sentinel-1:26379", "sentinel-2:26379"},
//	    Password:     os.Getenv("REDIS_PASSWORD"),
//	})
//
//	// Approach 2: Pass an adapter wrapping go-redis Sentinel client
//	rdb := goredis.NewFailoverClient(&goredis.FailoverOptions{...})
//	adapter := &redisrepo.SentinelAdapter{Client: rdb}
//	repo := redisrepo.NewIdempotencyRecordRepository(adapter)
//
//	// Approach 3: go-zero Redis with Sentinel config
//	// The go-zero framework supports Sentinel directly via its RedisConf:
//	//   conf := redis.RedisConf{Host: "sentinel-1:26379", Type: "sentinel", MasterName: "mymaster"}
//	//   rds := redis.MustNewRedis(conf)
//	//   repo := redisrepo.NewIdempotencyRecordRepository(rds)
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// SentinelConfig holds parameters for connecting to a Redis Sentinel deployment.
type SentinelConfig struct {
	// MasterName is the Sentinel master name (required).
	MasterName string

	// SentinelAddrs is the list of Sentinel node addresses (host:port).
	SentinelAddrs []string

	// Password for both the Sentinel nodes and the Redis master (optional).
	Password string

	// DB is the Redis database number (default: 0).
	DB int

	// DialTimeout is the connection timeout (default: 5s).
	DialTimeout time.Duration

	// ReadTimeout is the read timeout (default: 3s).
	ReadTimeout time.Duration
}

// NewSentinelClient creates a redisClient that connects through Redis Sentinel.
// The returned adapter implements the redisClient interface and can be passed
// directly to NewIdempotencyRecordRepository.
//
// Example:
//
//	client, err := redisrepo.NewSentinelClient(redisrepo.SentinelConfig{
//	    MasterName:    "mymaster",
//	    SentinelAddrs: []string{"sentinel-1:26379", "sentinel-2:26379", "sentinel-3:26379"},
//	})
//	repo := redisrepo.NewIdempotencyRecordRepository(client)
func NewSentinelClient(cfg SentinelConfig) (*SentinelAdapter, error) {
	if cfg.MasterName == "" {
		return nil, fmt.Errorf("redis sentinel: MasterName is required")
	}
	if len(cfg.SentinelAddrs) == 0 {
		return nil, fmt.Errorf("redis sentinel: at least one SentinelAddr is required")
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 3 * time.Second
	}

	rdb := goredis.NewFailoverClient(&goredis.FailoverOptions{
		MasterName:    cfg.MasterName,
		SentinelAddrs: cfg.SentinelAddrs,
		Password:      cfg.Password,
		DB:            cfg.DB,
		DialTimeout:   cfg.DialTimeout,
		ReadTimeout:   cfg.ReadTimeout,
	})

	return &SentinelAdapter{Client: rdb}, nil
}

// SentinelAdapter wraps a go-redis Sentinel/Failover client and adapts it to
// the redisClient interface expected by the repository.
//
// Use this when you already have a go-redis Failover client or when you need
// finer control over the connection parameters.
type SentinelAdapter struct {
	Client *goredis.Client
}

func (a *SentinelAdapter) GetCtx(ctx context.Context, key string) (string, error) {
	return a.Client.Get(ctx, key).Result()
}

func (a *SentinelAdapter) SetCtxEx(ctx context.Context, key, value string, seconds int) error {
	return a.Client.Set(ctx, key, value, time.Duration(seconds)*time.Second).Err()
}

func (a *SentinelAdapter) DelCtx(ctx context.Context, keys ...string) (int, error) {
	n, err := a.Client.Del(ctx, keys...).Result()
	return int(n), err
}

func (a *SentinelAdapter) ScriptRunCtx(ctx context.Context, script *goredis.Script, keys []string, args ...any) (any, error) {
	return script.Run(ctx, a.Client, keys, args...).Result()
}

var _ redisClient = (*SentinelAdapter)(nil)
