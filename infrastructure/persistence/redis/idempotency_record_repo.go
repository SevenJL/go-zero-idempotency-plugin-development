// Package redis provides an IdempotencyRecordRepository backed by Redis.
// It uses Lua scripts for atomic begin/commit/abort operations, making it
// safe for distributed deployments.
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

// redisClient is the subset of go-zero's *redis.Redis that this
// repository needs. It matches the methods on redis.RedisNode.
type redisClient interface {
	GetCtx(ctx context.Context, key string) (string, error)
	SetCtxEx(ctx context.Context, key, value string, seconds int) error
	DelCtx(ctx context.Context, keys ...string) (int, error)
	ScriptRunCtx(ctx context.Context, script *goredis.Script, keys []string, args ...any) (any, error)
}

// IdempotencyRecordRepository implements domain/repository.IdempotencyRecordRepository
// with Redis as the backing store.
type IdempotencyRecordRepository struct {
	client    redisClient
	keyPrefix string
}

// RepositoryOption configures the Redis repository.
type RepositoryOption func(*IdempotencyRecordRepository)

// WithKeyPrefix sets the Redis key prefix. Defaults to "idem".
func WithKeyPrefix(prefix string) RepositoryOption {
	return func(r *IdempotencyRecordRepository) {
		r.keyPrefix = prefix
	}
}

// NewIdempotencyRecordRepository creates a Redis-backed repository.
// The rds parameter must be a *redis.Redis from go-zero's
// github.com/zeromicro/go-zero/core/stores/redis package.
func NewIdempotencyRecordRepository(rds redisClient, opts ...RepositoryOption) *IdempotencyRecordRepository {
	repo := &IdempotencyRecordRepository{
		client:    rds,
		keyPrefix: "idem",
	}
	for _, opt := range opts {
		opt(repo)
	}
	return repo
}

// redisKey builds the full Redis key including the prefix.
func (r *IdempotencyRecordRepository) redisKey(key string) string {
	return fmt.Sprintf("%s:%s", r.keyPrefix, key)
}

// TryBegin implements repository.IdempotencyRecordRepository.
func (r *IdempotencyRecordRepository) TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error) {
	recordJSON, err := marshalRecord(record)
	if err != nil {
		return model.BeginDecision{}, fmt.Errorf("redis: marshal record: %w", err)
	}

	storeKey := r.redisKey(record.Key().String())
	ttl := int(record.ExpiresAt().Sub(record.CreatedAt()).Seconds())
	if ttl <= 0 {
		ttl = 30 // fallback to default processing TTL
	}

	result, err := r.client.ScriptRunCtx(ctx, beginScript, []string{storeKey},
		string(recordJSON),
		ttl,
		record.Fingerprint().String(),
	)
	if err != nil {
		return model.BeginDecision{}, fmt.Errorf("redis: begin script: %w", err)
	}

	payload := []byte(luaPayload(result))
	switch luaResult(result) {
	case luaAcquired:
		existing, _ := unmarshalRecord(payload)
		return model.Acquired(existing), nil
	case luaReplay:
		existing, _ := unmarshalRecord(payload)
		return model.Replay(existing), nil
	case luaConflict:
		existing, _ := unmarshalRecord(payload)
		return model.Conflict(existing), nil
	case luaInProgress:
		existing, _ := unmarshalRecord(payload)
		return model.InProgress(existing), nil
	case luaFailed:
		existing, _ := unmarshalRecord(payload)
		return model.Failed(existing), nil
	default:
		return model.BeginDecision{}, fmt.Errorf("redis: unexpected begin result: %s", luaResult(result))
	}
}

// Commit implements repository.IdempotencyRecordRepository.
func (r *IdempotencyRecordRepository) Commit(ctx context.Context, record *model.IdempotencyRecord) error {
	recordJSON, err := marshalRecord(record)
	if err != nil {
		return fmt.Errorf("redis: marshal record: %w", err)
	}

	storeKey := r.redisKey(record.Key().String())
	ttl := int(time.Until(record.ExpiresAt()).Seconds())
	if ttl <= 0 {
		ttl = 86400 // fallback to default completed TTL (24h)
	}

	result, err := r.client.ScriptRunCtx(ctx, commitScript, []string{storeKey},
		record.Owner().String(),
		record.Fingerprint().String(),
		string(recordJSON),
		ttl,
	)
	if err != nil {
		return fmt.Errorf("redis: commit script: %w", err)
	}

	switch luaResult(result) {
	case luaCommitted:
		return nil
	case luaMissing:
		return model.ErrInvalidState
	case luaOwnerMismatch:
		return model.ErrOwnerMismatch
	case luaConflict:
		return model.ErrFingerprintConflict
	case luaInvalidState:
		return model.ErrInvalidState
	default:
		return fmt.Errorf("redis: unexpected commit result: %s", luaResult(result))
	}
}

// Abort implements repository.IdempotencyRecordRepository.
func (r *IdempotencyRecordRepository) Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error {
	storeKey := r.redisKey(key.String())

	var recordJSON string
	var failedTTL int

	if mode == model.FailureModeCache {
		// We don't have the full failed record here — Abort takes key/owner/mode.
		// For cache mode the caller should use Complete with a failed response.
		// This is consistent with the application layer's Abort method for cache mode
		// which fetches the record, calls MarkFailed, then Commit (not Abort).
		//
		// For delete/keep_processing_until_ttl modes, we run the abort Lua script.
		// For cache mode, the application layer already handles it via
		// record.MarkFailed + repo.Commit.
	}

	result, err := r.client.ScriptRunCtx(ctx, abortScript, []string{storeKey},
		owner.String(),
		string(mode),
		recordJSON,
		failedTTL,
	)
	if err != nil {
		return fmt.Errorf("redis: abort script: %w", err)
	}

	switch luaResult(result) {
	case luaDeleted, luaCached, "ok":
		return nil
	case luaOwnerMismatch:
		return model.ErrOwnerMismatch
	case luaInvalidState:
		return model.ErrInvalidState
	default:
		return fmt.Errorf("redis: unexpected abort result: %s", luaResult(result))
	}
}

// Find implements repository.IdempotencyRecordRepository.
func (r *IdempotencyRecordRepository) Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	storeKey := r.redisKey(key.String())
	data, err := r.client.GetCtx(ctx, storeKey)
	if err != nil {
		// Redis returns nil when the key does not exist; go-zero maps that
		// to redis.Nil. We treat it as "not found".
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis: find get: %w", err)
	}

	record, err := unmarshalRecord([]byte(data))
	if err != nil {
		return nil, fmt.Errorf("redis: unmarshal record: %w", err)
	}
	return record, nil
}

// Renew implements repository.IdempotencyRecordRepository.
// It uses a Lua script to atomically extend the TTL after validating
// owner and status.
func (r *IdempotencyRecordRepository) Renew(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, ttl time.Duration) error {
	storeKey := r.redisKey(key.String())
	seconds := int(ttl.Seconds())
	if seconds <= 0 {
		seconds = 30
	}

	result, err := r.client.ScriptRunCtx(ctx, renewScript, []string{storeKey},
		owner.String(),
		seconds,
	)
	if err != nil {
		return fmt.Errorf("redis: renew script: %w", err)
	}

	switch luaResult(result) {
	case luaRenewed, "ok":
		return nil
	case luaMissing:
		return nil // best-effort
	case luaOwnerMismatch:
		return model.ErrOwnerMismatch
	default:
		return fmt.Errorf("redis: unexpected renew result: %s", luaResult(result))
	}
}
