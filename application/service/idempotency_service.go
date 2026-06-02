package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/senvejl117/go-idempotency/application/command"
	"github.com/senvejl117/go-idempotency/application/dto"
	"github.com/senvejl117/go-idempotency/application/port"
	"github.com/senvejl117/go-idempotency/domain/model"
	"github.com/senvejl117/go-idempotency/domain/repository"
	domainservice "github.com/senvejl117/go-idempotency/domain/service"
	"github.com/senvejl117/go-idempotency/domain/valueobject"
)

var ErrRepositoryRequired = errors.New("idempotency repository is required")

type IdempotencyService struct {
	enabled bool
	scope   string

	repo repository.IdempotencyRecordRepository

	keyResolver   port.KeyResolver
	fingerprinter port.Fingerprinter
	ownerFactory  port.OwnerFactory
	clock         port.Clock

	policy       domainservice.IdempotencyPolicy
	captureRules domainservice.CaptureRules
	waitTimeout  time.Duration
	waitInterval time.Duration

	logger  port.Logger
	metrics port.Metrics
	tracer  port.Tracer
}

func NewIdempotencyService(config Config) (*IdempotencyService, error) {
	config = config.normalized()
	if config.Repository == nil {
		return nil, ErrRepositoryRequired
	}

	return &IdempotencyService{
		enabled:       !config.Disabled,
		scope:         config.Scope,
		repo:          config.Repository,
		keyResolver:   config.KeyResolver,
		fingerprinter: config.Fingerprinter,
		ownerFactory:  config.OwnerFactory,
		clock:         config.Clock,
		policy:        config.Policy,
		captureRules:  config.CaptureRules,
		waitTimeout:   config.WaitTimeout,
		waitInterval:  config.WaitInterval,
		logger:        config.Logger,
		metrics:       config.Metrics,
		tracer:        config.Tracer,
	}, nil
}

func (s *IdempotencyService) Begin(ctx context.Context, cmd command.BeginCommand) (dto.BeginResult, error) {
	if !s.enabled {
		return dto.BeginResult{Type: dto.BeginResultSkipped}, nil
	}

	now := cmd.Now
	if now.IsZero() {
		now = s.clock.Now()
	}

	req := cmd.Request
	if req.Scope.Service == "" {
		req.Scope.Service = s.scope
	}

	key, err := s.keyResolver.Resolve(ctx, req)
	if err != nil {
		return dto.BeginResult{}, err
	}
	if key.IsZero() {
		return dto.BeginResult{Type: dto.BeginResultSkipped}, nil
	}

	fingerprint, err := s.fingerprinter.Fingerprint(ctx, req)
	if err != nil {
		return dto.BeginResult{}, err
	}

	owner, err := s.ownerFactory.NewOwner(ctx)
	if err != nil {
		return dto.BeginResult{}, err
	}

	record, err := model.NewProcessingRecord(model.NewRecordParams{
		Key:           key,
		Fingerprint:   fingerprint,
		Owner:         owner,
		Operation:     req.Operation,
		Scope:         req.Scope,
		Now:           now,
		ProcessingTTL: s.policy.TTL.ProcessingTTL,
	})
	if err != nil {
		return dto.BeginResult{}, err
	}

	decision, err := s.repo.TryBegin(ctx, record)
	if err != nil {
		return dto.BeginResult{}, err
	}
	if decision.Type == model.DecisionInProgress && s.policy.ShouldWaitDuplicate() && decision.Record != nil {
		replay, err := s.WaitReplay(ctx, command.ReplayCommand{
			Key:      decision.Record.Key(),
			Deadline: now.Add(s.waitTimeout),
		})
		if err != nil {
			return dto.BeginResult{}, err
		}
		if replay.Found {
			return beginResultFromReplay(key, fingerprint, owner, replay), nil
		}
		if replay.Record == nil {
			refreshed, err := model.NewProcessingRecord(model.NewRecordParams{
				Key:           key,
				Fingerprint:   fingerprint,
				Owner:         owner,
				Operation:     req.Operation,
				Scope:         req.Scope,
				Now:           s.clock.Now(),
				ProcessingTTL: s.policy.TTL.ProcessingTTL,
			})
			if err != nil {
				return dto.BeginResult{}, err
			}
			decision, err = s.repo.TryBegin(ctx, refreshed)
			if err != nil {
				return dto.BeginResult{}, err
			}
			return s.toBeginResult(key, fingerprint, owner, decision), nil
		}
	}

	return s.toBeginResult(key, fingerprint, owner, decision), nil
}

func (s *IdempotencyService) Complete(ctx context.Context, cmd command.CompleteCommand) error {
	now := cmd.Now
	if now.IsZero() {
		now = s.clock.Now()
	}

	// Find the record first — if it doesn't exist, fail early.
	record, err := s.repo.Find(ctx, cmd.Key)
	if err != nil {
		s.logger.Error(ctx, "idempotency complete find failed",
			port.Field{Key: "key_hash", Value: hashKey(cmd.Key.String())},
			port.Field{Key: "error", Value: err.Error()},
		)
		return err
	}
	if record == nil {
		return model.ErrInvalidState
	}

	resp := cmd.Response

	// Consult capture policy. If the response should not be cached, auto-abort
	// instead of completing. This keeps the domain model clean — the domain
	// aggregate does not need to know about HTTP status-code conventions.
	if !s.captureRules.ShouldCache(resp.StatusCode, contentType(resp.Headers), int64(len(resp.Body))) {
		return s.repo.Abort(ctx, cmd.Key, cmd.Owner, model.FailureModeDelete)
	}

	// Strip excluded headers before storing.
	resp.Headers = s.captureRules.FilterHeaders(resp.Headers)

	if err := record.Complete(cmd.Owner, cmd.Fingerprint, toDomainResponse(resp), now, s.policy.TTL.CompletedTTL); err != nil {
		return err
	}
	if err := s.repo.Commit(ctx, record); err != nil {
		s.logger.Error(ctx, "idempotency commit failed",
			port.Field{Key: "key_hash", Value: hashKey(cmd.Key.String())},
			port.Field{Key: "error", Value: err.Error()},
		)
		s.metrics.CounterIncrement("idempotency_commit_total", map[string]string{"result": "error"})
		return err
	}

	s.metrics.CounterIncrement("idempotency_commit_total", map[string]string{"result": "success"})
	return nil
}

func (s *IdempotencyService) Abort(ctx context.Context, cmd command.AbortCommand) error {
	mode := cmd.Mode
	if mode == "" {
		mode = model.FailureModeDelete
	}
	if mode == model.FailureModeDelete || mode == model.FailureModeKeepProcessingTTL {
		return s.repo.Abort(ctx, cmd.Key, cmd.Owner, mode)
	}

	now := cmd.Now
	if now.IsZero() {
		now = s.clock.Now()
	}

	record, err := s.repo.Find(ctx, cmd.Key)
	if err != nil {
		return err
	}
	if record == nil {
		return model.ErrInvalidState
	}
	if err := record.MarkFailed(cmd.Owner, cmd.Fingerprint, cmd.ErrorCode, cmd.ErrorMessage, toDomainResponse(cmd.Response), now, s.policy.TTL.FailedTTL); err != nil {
		return err
	}
	return s.repo.Commit(ctx, record)
}

func (s *IdempotencyService) WaitReplay(ctx context.Context, cmd command.ReplayCommand) (dto.ReplayResult, error) {
	deadline := cmd.Deadline
	if deadline.IsZero() {
		deadline = s.clock.Now().Add(s.waitTimeout)
	}

	for {
		select {
		case <-ctx.Done():
			return dto.ReplayResult{}, ctx.Err()
		default:
		}

		record, err := s.repo.Find(ctx, cmd.Key)
		if err != nil {
			return dto.ReplayResult{}, err
		}
		if record == nil {
			return dto.ReplayResult{Found: false, Key: cmd.Key}, nil
		}
		if record.Status() == model.StatusCompleted || record.Status() == model.StatusFailed {
			return dto.ReplayResult{
				Found:        true,
				Key:          cmd.Key,
				Record:       record,
				Response:     fromDomainResponse(record.Response()),
				ErrorCode:    record.ErrorCode(),
				ErrorMessage: record.ErrorMessage(),
			}, nil
		}
		now := s.clock.Now()
		if !now.Before(deadline) {
			return dto.ReplayResult{Found: false, Key: cmd.Key, Record: record}, nil
		}
		sleepFor := s.waitInterval
		if remaining := deadline.Sub(now); remaining < sleepFor {
			sleepFor = remaining
		}
		// Keep the polling loop in the application layer. Adapters decide how to
		// render the timeout, while repositories only expose the current record.
		s.clock.Sleep(sleepFor)
	}
}

func (s *IdempotencyService) toBeginResult(key valueobject.IdempotencyKey, fingerprint valueobject.Fingerprint, owner valueobject.Owner, decision model.BeginDecision) dto.BeginResult {
	result := dto.BeginResult{
		Key:         key,
		Fingerprint: fingerprint,
		Owner:       owner,
		Record:      decision.Record,
	}

	switch decision.Type {
	case model.DecisionAcquired:
		result.Type = dto.BeginResultAcquired
	case model.DecisionReplay:
		result.Type = dto.BeginResultReplay
		if decision.Record != nil {
			result.Response = fromDomainResponse(decision.Record.Response())
		}
	case model.DecisionConflict:
		result.Type = dto.BeginResultConflict
	case model.DecisionInProgress:
		result.Type = dto.BeginResultInProgress
	case model.DecisionFailed:
		result.Type = dto.BeginResultFailed
		if decision.Record != nil {
			result.Response = fromDomainResponse(decision.Record.Response())
			result.ErrorCode = decision.Record.ErrorCode()
			result.ErrorMessage = decision.Record.ErrorMessage()
		}
	default:
		result.Type = dto.BeginResultInProgress
	}

	return result
}

func beginResultFromReplay(key valueobject.IdempotencyKey, fingerprint valueobject.Fingerprint, owner valueobject.Owner, replay dto.ReplayResult) dto.BeginResult {
	resultType := dto.BeginResultReplay
	if replay.Record != nil && replay.Record.Status() == model.StatusFailed {
		resultType = dto.BeginResultFailed
	}

	return dto.BeginResult{
		Type:         resultType,
		Key:          key,
		Fingerprint:  fingerprint,
		Owner:        owner,
		Record:       replay.Record,
		Response:     replay.Response,
		ErrorCode:    replay.ErrorCode,
		ErrorMessage: replay.ErrorMessage,
	}
}

func toDomainResponse(response dto.CapturedResponse) model.CapturedResponse {
	return model.CapturedResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Headers,
		Body:       response.Body,
		Codec:      response.Codec,
	}
}

func fromDomainResponse(response model.CapturedResponse) dto.CapturedResponse {
	return dto.CapturedResponse{
		StatusCode: response.StatusCode,
		Headers:    response.Headers,
		Body:       response.Body,
		Codec:      response.Codec,
	}
}

// hashKey returns a truncated hex SHA-256 prefix of the raw key.
// The full idempotency key must never appear in logs.
func hashKey(raw string) string {
	if raw == "" {
		return "<empty>"
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:12]
}

// contentType extracts the Content-Type value from headers in a
// case-insensitive manner.
func contentType(headers map[string][]string) string {
	for name, values := range headers {
		if len(values) > 0 && equalFold(name, "Content-Type") {
			return values[0]
		}
	}
	return ""
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
