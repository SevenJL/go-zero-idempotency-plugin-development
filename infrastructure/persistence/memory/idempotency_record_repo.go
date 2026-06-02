package memory

import (
	"context"
	"sync"
	"time"

	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

type IdempotencyRecordRepository struct {
	mu      sync.Mutex
	records map[string]*model.IdempotencyRecord
	now     func() time.Time
}

type Option func(*IdempotencyRecordRepository)

func WithClock(now func() time.Time) Option {
	return func(repo *IdempotencyRecordRepository) {
		if now != nil {
			repo.now = now
		}
	}
}

func NewIdempotencyRecordRepository(opts ...Option) *IdempotencyRecordRepository {
	repo := &IdempotencyRecordRepository{
		records: make(map[string]*model.IdempotencyRecord),
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, opt := range opts {
		opt(repo)
	}
	return repo
}

// TryBegin implements the "try begin" logic for idempotency records, mirroring the Redis Lua script:
// - If no record exists or the existing record is expired, create a new processing record and return Acquired.
// - If a non-expired record exists and conflicts with the new request, return Conflict.
// - If a non-expired record exists and does not conflict, return its status (Replay, Failed, or InProgress).
func (r *IdempotencyRecordRepository) TryBegin(_ context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := record.Key().String()
	existing := r.records[key]
	if existing == nil || existing.IsExpired(r.now()) {
		// This branch mirrors the Redis Lua begin script: check absence/expiry
		// and write the processing record under the same lock.
		r.records[key] = record.Clone()
		return model.Acquired(record.Clone()), nil
	}

	if existing.ConflictsWith(record.Fingerprint()) {
		return model.Conflict(existing.Clone()), nil
	}

	switch existing.Status() {
	case model.StatusCompleted:
		return model.Replay(existing.Clone()), nil
	case model.StatusFailed:
		return model.Failed(existing.Clone()), nil
	default:
		return model.InProgress(existing.Clone()), nil
	}
}

func (r *IdempotencyRecordRepository) Commit(_ context.Context, record *model.IdempotencyRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := record.Key().String()
	existing := r.records[key]
	if existing == nil {
		return model.ErrInvalidState
	}
	if existing.IsExpired(r.now()) {
		delete(r.records, key)
		return model.ErrInvalidState
	}
	if !existing.Owner().Equals(record.Owner()) {
		return model.ErrOwnerMismatch
	}
	if existing.ConflictsWith(record.Fingerprint()) {
		return model.ErrFingerprintConflict
	}
	if existing.Status() != model.StatusProcessing {
		return model.ErrInvalidState
	}
	if record.Status() != model.StatusCompleted && record.Status() != model.StatusFailed {
		return model.ErrInvalidState
	}

	r.records[key] = record.Clone()
	return nil
}

func (r *IdempotencyRecordRepository) Abort(_ context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing := r.records[key.String()]
	if existing == nil {
		return nil
	}
	if existing.IsExpired(r.now()) {
		delete(r.records, key.String())
		return model.ErrInvalidState
	}
	if !existing.Owner().Equals(owner) {
		return model.ErrOwnerMismatch
	}
	if existing.Status() != model.StatusProcessing {
		return model.ErrInvalidState
	}

	if mode == model.FailureModeDelete {
		delete(r.records, key.String())
	}
	return nil
}

func (r *IdempotencyRecordRepository) Find(_ context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing := r.records[key.String()]
	if existing == nil {
		return nil, nil
	}
	if existing.IsExpired(r.now()) {
		delete(r.records, key.String())
		return nil, nil
	}
	return existing.Clone(), nil
}
