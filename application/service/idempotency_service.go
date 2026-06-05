package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/repository"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
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

	logger   port.Logger
	metrics  port.Metrics
	tracer   port.Tracer
	notifier port.Notifier
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
		notifier:      config.Notifier,
	}, nil
}

// Begin initiates an idempotent operation. It resolves the idempotency key
// from the request headers, computes a fingerprint of the request body, and
// attempts to acquire exclusive ownership via the repository.
//
// Possible outcomes:
//   - BeginResultSkipped  — service is disabled or no idempotency key present
//   - BeginResultAcquired — new record created, caller should execute the handler
//   - BeginResultReplay   — previous completed response available for replay
//   - BeginResultConflict — same key but different request body (fingerprint mismatch)
//   - BeginResultInProgress — another caller is already processing this key
//   - BeginResultFailed   — previous attempt failed, error details available
//
// When the duplicate policy is DuplicateWait and a record is in_progress,
// Begin will block via WaitReplay until the record resolves or times out.
func (s *IdempotencyService) Begin(ctx context.Context, cmd command.BeginCommand) (dto.BeginResult, error) {
	if !s.enabled {
		return dto.BeginResult{Type: dto.BeginResultSkipped}, nil
	}

	now := cmd.Now
	if now.IsZero() {
		now = s.clock.Now()
	}

	req := cmd.Request
	if req.Scope.Service() == "" {
		req.Scope = req.Scope.WithService(s.scope)
	}

	key, err := s.keyResolver.Resolve(ctx, req)
	if err != nil {
		return dto.BeginResult{}, fmt.Errorf("begin: resolve key: %w", err)
	}
	if key.IsZero() {
		return dto.BeginResult{Type: dto.BeginResultSkipped}, nil
	}

	fingerprint, err := s.fingerprinter.Fingerprint(ctx, req)
	if err != nil {
		return dto.BeginResult{}, fmt.Errorf("begin: fingerprint: %w", err)
	}

	owner, err := s.ownerFactory.NewOwner(ctx)
	if err != nil {
		return dto.BeginResult{}, fmt.Errorf("begin: new owner: %w", err)
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
		return dto.BeginResult{}, fmt.Errorf("begin: new record: %w", err)
	}

	decision, err := s.repo.TryBegin(ctx, record)
	if err != nil {
		return dto.BeginResult{}, fmt.Errorf("begin: try begin: %w", err)
	}
	if decision.Type == model.DecisionInProgress && s.policy.ShouldWaitDuplicate() && decision.Record != nil {
		replay, err := s.WaitReplay(ctx, command.ReplayCommand{
			Key:      decision.Record.Key(),
			Deadline: now.Add(s.waitTimeout),
		})
		if err != nil {
			return dto.BeginResult{}, fmt.Errorf("begin: wait replay: %w", err)
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
				return dto.BeginResult{}, fmt.Errorf("begin: new record (retry): %w", err)
			}
			decision, err = s.repo.TryBegin(ctx, refreshed)
			if err != nil {
				return dto.BeginResult{}, fmt.Errorf("begin: try begin (retry): %w", err)
			}
			return s.toBeginResult(key, fingerprint, owner, decision), nil
		}
	}

	return s.toBeginResult(key, fingerprint, owner, decision), nil
}

// Complete finalises a successfully processed idempotent operation. It stores
// the handler's response so subsequent requests with the same key can replay.
//
// Before committing, the capture policy is consulted — if the response status
// code or content type is not cacheable, the record is aborted (deleted)
// instead. Excluded headers (e.g. Set-Cookie, Authorization) are stripped
// from the stored response.
//
// Errors:
//   - ErrInvalidState      — record not found or not in processing state
//   - ErrOwnerMismatch     — the owner does not match the original Begin caller
//   - ErrFingerprintConflict — the fingerprint does not match
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
		return fmt.Errorf("complete: find: %w", err)
	}
	if record == nil {
		return model.ErrInvalidState
	}

	resp := cmd.Response

	// Consult capture policy. If the response should not be cached, auto-abort
	// instead of completing. This keeps the domain model clean — the domain
	// aggregate does not need to know about HTTP status-code conventions.
	if !s.captureRules.ShouldCache(resp.StatusCode, contentType(resp.Headers), int64(len(resp.Body))) {
		if err := s.repo.Abort(ctx, cmd.Key, cmd.Owner, model.FailureModeDelete); err != nil {
			s.logger.Error(ctx, "idempotency abort (not cacheable) failed",
				port.Field{Key: "key_hash", Value: hashKey(cmd.Key.String())},
				port.Field{Key: "error", Value: err.Error()},
			)
			return fmt.Errorf("complete: abort (not cacheable): %w", err)
		}
		return nil
	}

	// Strip excluded headers before storing.
	resp.Headers = s.captureRules.FilterHeaders(resp.Headers)

	if err := record.Complete(cmd.Owner, cmd.Fingerprint, toDomainResponse(resp), now, s.policy.TTL.CompletedTTL); err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	if err := s.repo.Commit(ctx, record); err != nil {
		s.logger.Error(ctx, "idempotency commit failed",
			port.Field{Key: "key_hash", Value: hashKey(cmd.Key.String())},
			port.Field{Key: "error", Value: err.Error()},
		)
		s.metrics.CounterIncrementContext(ctx, "idempotency_commit_total", map[string]string{"result": "error"})
		return fmt.Errorf("complete: commit: %w", err)
	}

	s.metrics.CounterIncrementContext(ctx, "idempotency_commit_total", map[string]string{"result": "success"})
	return nil
}

// Abort handles a failed idempotent operation according to the configured
// failure mode:
//
//   - FailureModeDelete: the record is removed, allowing a fresh Begin on retry
//   - FailureModeCache: the error is stored so subsequent requests can replay
//     the failure (e.g. for consistent error messages)
//   - FailureModeKeepProcessingTTL: the record is left as-is; it expires
//     naturally after the processing TTL
//
// When mode is empty, FailureModeDelete is used as the default.
// For FailureModeCache, the error details are persisted via MarkFailed.
func (s *IdempotencyService) Abort(ctx context.Context, cmd command.AbortCommand) error {
	mode := cmd.Mode
	if mode == "" {
		mode = model.FailureModeDelete
	}
	if mode == model.FailureModeDelete || mode == model.FailureModeKeepProcessingTTL {
		if err := s.repo.Abort(ctx, cmd.Key, cmd.Owner, mode); err != nil {
			s.logger.Error(ctx, "idempotency abort failed",
				port.Field{Key: "key_hash", Value: hashKey(cmd.Key.String())},
				port.Field{Key: "error", Value: err.Error()},
			)
			return fmt.Errorf("abort: %w", err)
		}
		return nil
	}

	now := cmd.Now
	if now.IsZero() {
		now = s.clock.Now()
	}

	record, err := s.repo.Find(ctx, cmd.Key)
	if err != nil {
		return fmt.Errorf("abort: find: %w", err)
	}
	if record == nil {
		return model.ErrInvalidState
	}
	if err := record.MarkFailed(cmd.Owner, cmd.Fingerprint, cmd.ErrorCode, cmd.ErrorMessage, toDomainResponse(cmd.Response), now, s.policy.TTL.FailedTTL); err != nil {
		return fmt.Errorf("abort: mark failed: %w", err)
	}
	if err := s.repo.Commit(ctx, record); err != nil {
		return fmt.Errorf("abort: commit: %w", err)
	}
	return nil
}

// WaitReplay blocks until the record identified by Key reaches a terminal
// state (Completed or Failed) or the deadline expires. It uses a dual-path
// strategy:
//
//   - Fast path: subscribes to Redis Pub/Sub notifications (via the Notifier
//     port) and returns immediately when a state-change event is received.
//   - Fallback: polls the repository at WaitInterval until the deadline,
//     then returns Found=false with a copy of the still-processing record.
//
// When Found is true, the caller can replay the cached response or error.
// When Found is false and Record is nil, the previous record expired and
// the caller should retry TryBegin with a fresh record.
func (s *IdempotencyService) WaitReplay(ctx context.Context, cmd command.ReplayCommand) (dto.ReplayResult, error) {
	deadline := cmd.Deadline
	if deadline.IsZero() {
		deadline = s.clock.Now().Add(s.waitTimeout)
	}

	// Check initial state
	record, err := s.repo.Find(ctx, cmd.Key)
	if err != nil {
		return dto.ReplayResult{}, fmt.Errorf("wait replay: find: %w", err)
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

	// Try Pub/Sub notification if available — falls through to polling
	// if the notifier doesn't deliver or the context times out.
	channel := "idempotency:events:" + cmd.Key.String()

	// Create a context that respects both the deadline and the caller's context.
	notifyCtx, cancelNotify := context.WithDeadline(ctx, deadline)

	// Attempt notification-based wait concurrently with polling.
	// If the notifier delivers first, we short-circuit.
	notifyCh := make(chan dto.ReplayResult, 1)
	notifyDone := make(chan struct{})
	go func() {
		defer close(notifyDone)
		msg, err := s.notifier.Wait(notifyCtx, channel)
		if err != nil {
			return // context cancelled or deadline exceeded
		}
		_ = msg // message signals state change; we re-check the record below
		record, err := s.repo.Find(ctx, cmd.Key)
		if err != nil {
			return
		}
		if record == nil {
			return
		}
		if record.Status() == model.StatusCompleted || record.Status() == model.StatusFailed {
			select {
			case notifyCh <- dto.ReplayResult{
				Found:        true,
				Key:          cmd.Key,
				Record:       record,
				Response:     fromDomainResponse(record.Response()),
				ErrorCode:    record.ErrorCode(),
				ErrorMessage: record.ErrorMessage(),
			}:
			case <-ctx.Done():
			}
		}
	}()
	// Ensure the notify goroutine exits before WaitReplay returns.
	defer func() {
		cancelNotify()
		<-notifyDone
	}()

	// Polling loop as fallback
	for {
		select {
		case result := <-notifyCh:
			return result, nil
		case <-ctx.Done():
			return dto.ReplayResult{}, fmt.Errorf("wait replay: %w", ctx.Err())
		default:
		}

		record, err := s.repo.Find(ctx, cmd.Key)
		if err != nil {
			return dto.ReplayResult{}, fmt.Errorf("wait replay: poll: %w", err)
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
