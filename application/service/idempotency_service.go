package service

import (
	"context"
	"errors"
	"time"

	"github.com/your-org/go-idempotency/application/command"
	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/application/port"
	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/repository"
	domainservice "github.com/your-org/go-idempotency/domain/service"
	"github.com/your-org/go-idempotency/domain/valueobject"
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
	waitTimeout  time.Duration
	waitInterval time.Duration
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
		waitTimeout:   config.WaitTimeout,
		waitInterval:  config.WaitInterval,
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

	record, err := s.repo.Find(ctx, cmd.Key)
	if err != nil {
		return err
	}
	if record == nil {
		return model.ErrInvalidState
	}

	if err := record.Complete(cmd.Owner, cmd.Fingerprint, toDomainResponse(cmd.Response), now, s.policy.TTL.CompletedTTL); err != nil {
		return err
	}
	return s.repo.Commit(ctx, record)
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
