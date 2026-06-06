// Package sql provides an IdempotencyRecordRepository backed by a relational
// database (MySQL or PostgreSQL). It uses INSERT ... ON DUPLICATE KEY (MySQL)
// or INSERT ... ON CONFLICT (PostgreSQL) for atomic TryBegin semantics.
//
// The repository requires the schema defined in schema.sql. Expired record
// cleanup runs in a background goroutine by default; call Close during
// graceful shutdown to stop it.
package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

// Driver identifies the SQL dialect.
type Driver string

const (
	DriverMySQL    Driver = "mysql"
	DriverPostgres Driver = "postgres"
)

// IdempotencyRecordRepository implements domain/repository.IdempotencyRecordRepository
// with a relational database as the backing store.
type IdempotencyRecordRepository struct {
	db     *sql.DB
	driver Driver

	cleanupInterval time.Duration
	cleanupDone     chan struct{}
	cleanupOnce     sync.Once
}

// NewIdempotencyRecordRepository creates a SQL-backed repository.
// Expired records are cleaned up every 60s in a background goroutine.
// Use NewIdempotencyRecordRepositoryWithCleanup to customise the interval.
func NewIdempotencyRecordRepository(db *sql.DB, driver Driver) *IdempotencyRecordRepository {
	return NewIdempotencyRecordRepositoryWithCleanup(db, driver, 60*time.Second)
}

// NewIdempotencyRecordRepositoryWithCleanup creates a SQL-backed repository
// with a custom cleanup interval. Set interval to 0 to disable background
// cleanup (caller is then responsible for periodic expired-record cleanup).
func NewIdempotencyRecordRepositoryWithCleanup(db *sql.DB, driver Driver, interval time.Duration) *IdempotencyRecordRepository {
	r := &IdempotencyRecordRepository{
		db:              db,
		driver:          driver,
		cleanupInterval: interval,
	}
	if interval > 0 {
		r.cleanupDone = make(chan struct{})
		go r.cleanupLoop()
	}
	return r
}

// Close stops the background cleanup goroutine. Safe to call multiple times.
// After Close returns, no more cleanup queries will be executed.
func (r *IdempotencyRecordRepository) Close() {
	r.cleanupOnce.Do(func() {
		if r.cleanupDone != nil {
			close(r.cleanupDone)
		}
	})
}

func (r *IdempotencyRecordRepository) cleanupLoop() {
	ticker := time.NewTicker(r.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.cleanupDone:
			return
		case <-ticker.C:
			r.deleteExpired(context.Background())
		}
	}
}

func (r *IdempotencyRecordRepository) TryBegin(ctx context.Context, record *model.IdempotencyRecord) (model.BeginDecision, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return model.BeginDecision{}, fmt.Errorf("sql: begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op if already committed
	}()

	headersJSON, _ := json.Marshal(record.Response().Headers)

	inserted, err := r.insertRecordTx(ctx, tx, record, string(headersJSON))
	if err != nil {
		return model.BeginDecision{}, fmt.Errorf("sql: insert record: %w", err)
	}
	if inserted {
		if commitErr := tx.Commit(); commitErr != nil {
			return model.BeginDecision{}, fmt.Errorf("sql: commit tx: %w", commitErr)
		}
		return model.Acquired(record), nil
	}

	existing, err := r.findTx(ctx, tx, record.Key(), record.Scope().Service())
	if err != nil {
		return model.BeginDecision{}, fmt.Errorf("sql: find duplicate record: %w", err)
	}
	if existing == nil {
		if err := r.deleteExpiredByKeyTx(ctx, tx, record.Key(), record.Scope().Service()); err != nil {
			return model.BeginDecision{}, fmt.Errorf("sql: delete expired duplicate: %w", err)
		}
		inserted, err = r.insertRecordTx(ctx, tx, record, string(headersJSON))
		if err != nil {
			return model.BeginDecision{}, fmt.Errorf("sql: retry insert record: %w", err)
		}
		if inserted {
			if commitErr := tx.Commit(); commitErr != nil {
				return model.BeginDecision{}, fmt.Errorf("sql: commit tx: %w", commitErr)
			}
			return model.Acquired(record), nil
		}
		existing, err = r.findTx(ctx, tx, record.Key(), record.Scope().Service())
		if err != nil {
			return model.BeginDecision{}, fmt.Errorf("sql: find duplicate record after retry: %w", err)
		}
		if existing == nil {
			return model.BeginDecision{}, fmt.Errorf("sql: duplicate key but active record not found")
		}
	}

	if err := tx.Commit(); err != nil {
		return model.BeginDecision{}, fmt.Errorf("sql: commit tx: %w", err)
	}
	return beginDecision(existing, record.Fingerprint()), nil
}

func beginDecision(existing *model.IdempotencyRecord, fingerprint valueobject.Fingerprint) model.BeginDecision {
	if existing.ConflictsWith(fingerprint) {
		return model.Conflict(existing)
	}

	switch existing.Status() {
	case model.StatusProcessing:
		return model.InProgress(existing)
	case model.StatusCompleted:
		return model.Replay(existing)
	case model.StatusFailed:
		return model.Failed(existing)
	default:
		return model.InProgress(existing)
	}
}

func (r *IdempotencyRecordRepository) Commit(ctx context.Context, record *model.IdempotencyRecord) error {
	headersJSON, _ := json.Marshal(record.Response().Headers)
	resp := record.Response()
	expiresAt := record.ExpiresAt().Format("2006-01-02 15:04:05.000")

	result, err := r.db.ExecContext(ctx, r.updateQuery(),
		record.Status().String(),
		resp.StatusCode,
		string(headersJSON),
		string(resp.Body),
		resp.Codec,
		record.ErrorCode(),
		record.ErrorMessage(),
		expiresAt,
		record.Key().String(),
		record.Scope().Service(),
		record.Owner().String(),
		record.Fingerprint().String(),
		model.StatusProcessing.String(),
	)
	if err != nil {
		return fmt.Errorf("sql: commit: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sql: commit rows affected: %w", err)
	}
	if n == 0 {
		return r.commitMissError(ctx, record)
	}
	return nil
}

func (r *IdempotencyRecordRepository) Abort(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, mode model.FailureMode) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("sql: abort begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	existing, err := r.FindTx(ctx, tx, key)
	if err != nil {
		return fmt.Errorf("sql: abort find: %w", err)
	}
	if existing == nil {
		_ = tx.Commit()
		return nil
	}
	if !existing.Owner().Equals(owner) {
		return model.ErrOwnerMismatch
	}
	if existing.Status() != model.StatusProcessing {
		return model.ErrInvalidState
	}

	switch mode {
	case model.FailureModeDelete:
		if err := r.execRowsAffected(ctx, tx, r.deleteByOwnerQuery(), existing.Scope().Service(), key.String(), owner.String()); err != nil {
			return fmt.Errorf("sql: abort delete: %w", err)
		}
	case model.FailureModeCache:
		if err := r.execRowsAffected(ctx, tx, r.markFailedQuery(), existing.Scope().Service(), key.String(), owner.String()); err != nil {
			return fmt.Errorf("sql: abort cache: %w", err)
		}
	case model.FailureModeKeepProcessingTTL:
		// The record remains in processing state until its TTL expires.
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sql: abort commit: %w", err)
	}
	return nil
}

func (r *IdempotencyRecordRepository) Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	return r.findTx(ctx, r.db, key, "")
}

func (r *IdempotencyRecordRepository) Renew(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl).Format("2006-01-02 15:04:05.000")
	_, err := r.db.ExecContext(ctx, r.renewQuery(), expiresAt, key.String(), owner.String())
	return err
}

// ---- Query builders ----

func (r *IdempotencyRecordRepository) insertQuery() string {
	if r.driver == DriverPostgres {
		return `INSERT INTO idempotency_records
			(idempotency_key, fingerprint, owner, operation, scope_service, scope_tenant, scope_user,
			 status, resp_headers, resp_body, resp_codec, expires_at, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
		 ON CONFLICT (scope_service, idempotency_key) DO NOTHING`
	}
	return `INSERT INTO idempotency_records
		(idempotency_key, fingerprint, owner, operation, scope_service, scope_tenant, scope_user,
		 status, resp_headers, resp_body, resp_codec, expires_at, created_at, updated_at)
	 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NOW(3),NOW(3))
	 ON DUPLICATE KEY UPDATE id=id`
}

func (r *IdempotencyRecordRepository) updateQuery() string {
	if r.driver == DriverPostgres {
		return `UPDATE idempotency_records
			SET status = $1, status_code = $2, resp_headers = $3, resp_body = $4, resp_codec = $5,
			    error_code = $6, error_message = $7, expires_at = $8, updated_at = NOW()
			WHERE idempotency_key = $9 AND scope_service = $10
			  AND owner = $11 AND fingerprint = $12 AND status = $13`
	}
	return `UPDATE idempotency_records
		SET status = ?, status_code = ?, resp_headers = ?, resp_body = ?, resp_codec = ?,
		    error_code = ?, error_message = ?, expires_at = ?, updated_at = NOW(3)
		WHERE idempotency_key = ? AND scope_service = ?
		  AND owner = ? AND fingerprint = ? AND status = ?`
}

func (r *IdempotencyRecordRepository) findQuery(scopeScoped bool) string {
	where := "WHERE idempotency_key = ? AND expires_at > NOW()"
	if scopeScoped {
		where = "WHERE idempotency_key = ? AND scope_service = ? AND expires_at > NOW()"
	}
	if r.driver == DriverPostgres {
		where = "WHERE idempotency_key = $1 AND expires_at > NOW()"
		if scopeScoped {
			where = "WHERE idempotency_key = $1 AND scope_service = $2 AND expires_at > NOW()"
		}
	}
	return `SELECT idempotency_key, fingerprint, owner, operation, scope_service, scope_tenant, scope_user,
	        status, status_code, resp_headers, resp_body, resp_codec, error_code, error_message,
	        created_at, updated_at, expires_at
	 FROM idempotency_records
	 ` + where + `
	 ORDER BY created_at DESC LIMIT 1`
}

func (r *IdempotencyRecordRepository) deleteExpiredByKeyQuery() string {
	if r.driver == DriverPostgres {
		return `DELETE FROM idempotency_records WHERE scope_service = $1 AND idempotency_key = $2 AND expires_at <= NOW()`
	}
	return `DELETE FROM idempotency_records WHERE scope_service = ? AND idempotency_key = ? AND expires_at <= NOW()`
}

func (r *IdempotencyRecordRepository) deleteByOwnerQuery() string {
	if r.driver == DriverPostgres {
		return `DELETE FROM idempotency_records WHERE scope_service = $1 AND idempotency_key = $2 AND owner = $3`
	}
	return `DELETE FROM idempotency_records WHERE scope_service = ? AND idempotency_key = ? AND owner = ?`
}

func (r *IdempotencyRecordRepository) markFailedQuery() string {
	if r.driver == DriverPostgres {
		return `UPDATE idempotency_records SET status = 'failed', updated_at = NOW() WHERE scope_service = $1 AND idempotency_key = $2 AND owner = $3`
	}
	return `UPDATE idempotency_records SET status = 'failed', updated_at = NOW(3) WHERE scope_service = ? AND idempotency_key = ? AND owner = ?`
}

func (r *IdempotencyRecordRepository) renewQuery() string {
	if r.driver == DriverPostgres {
		return `UPDATE idempotency_records SET expires_at = $1 WHERE idempotency_key = $2 AND owner = $3 AND status = 'processing'`
	}
	return `UPDATE idempotency_records SET expires_at = ? WHERE idempotency_key = ? AND owner = ? AND status = 'processing'`
}

func (r *IdempotencyRecordRepository) cleanupQuery() string {
	return `DELETE FROM idempotency_records WHERE expires_at < NOW()`
}

func (r *IdempotencyRecordRepository) insertRecord(ctx context.Context, record *model.IdempotencyRecord, headersJSON string) (bool, error) {
	return r.insertRecordTx(ctx, r.db, record, headersJSON)
}

func (r *IdempotencyRecordRepository) insertRecordTx(ctx context.Context, exec execContext, record *model.IdempotencyRecord, headersJSON string) (bool, error) {
	resp := record.Response()
	expiresAt := record.ExpiresAt().Format("2006-01-02 15:04:05.000")
	now := record.CreatedAt().Format("2006-01-02 15:04:05.000")

	var result sql.Result
	var err error
	if r.driver == DriverPostgres {
		result, err = exec.ExecContext(ctx, r.insertQuery(),
			record.Key().String(), record.Fingerprint().String(), record.Owner().String(),
			record.Operation().String(), record.Scope().Service(), record.Scope().Tenant(), record.Scope().User(),
			record.Status().String(), headersJSON, string(resp.Body), resp.Codec,
			expiresAt, now,
		)
	} else {
		result, err = exec.ExecContext(ctx, r.insertQuery(),
			record.Key().String(), record.Fingerprint().String(), record.Owner().String(),
			record.Operation().String(), record.Scope().Service(), record.Scope().Tenant(), record.Scope().User(),
			record.Status().String(), headersJSON, string(resp.Body), resp.Codec,
			expiresAt,
		)
	}
	if err != nil {
		return false, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sql: rows affected: %w", err)
	}
	return rows == 1, nil
}

// execContext abstracts *sql.DB and *sql.Tx for queries.
type execContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// FindTx reads a record within a transaction.
func (r *IdempotencyRecordRepository) FindTx(ctx context.Context, exec execContext, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	return r.findTx(ctx, exec, key, "")
}

func (r *IdempotencyRecordRepository) findTx(ctx context.Context, exec execContext, key valueobject.IdempotencyKey, scopeService string) (*model.IdempotencyRecord, error) {
	args := []any{key.String()}
	if scopeService != "" {
		args = append(args, scopeService)
	}

	row := exec.QueryRowContext(ctx, r.findQuery(scopeService != ""), args...)

	var rec sqlRecord
	var headersStr string
	err := row.Scan(
		&rec.Key, &rec.Fingerprint, &rec.Owner, &rec.Operation,
		&rec.ScopeService, &rec.ScopeTenant, &rec.ScopeUser,
		&rec.Status, &rec.StatusCode, &headersStr, &rec.RespBody, &rec.RespCodec,
		&rec.ErrorCode, &rec.ErrorMessage,
		&rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sql: find: %w", err)
	}

	rec.RespHeaders = headersStr
	return rec.toDomain()
}

var _ execContext = (*sql.DB)(nil)
var _ execContext = (*sql.Tx)(nil)

func (r *IdempotencyRecordRepository) deleteExpiredByKeyTx(ctx context.Context, exec execContext, key valueobject.IdempotencyKey, scopeService string) error {
	_, err := exec.ExecContext(ctx, r.deleteExpiredByKeyQuery(), scopeService, key.String())
	return err
}

func (r *IdempotencyRecordRepository) deleteExpired(ctx context.Context) {
	_, _ = r.db.ExecContext(ctx, r.cleanupQuery())
}

func (r *IdempotencyRecordRepository) execRowsAffected(ctx context.Context, exec execContext, query string, args ...any) error {
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrInvalidState
	}
	return nil
}

func (r *IdempotencyRecordRepository) commitMissError(ctx context.Context, record *model.IdempotencyRecord) error {
	existing, err := r.findTx(ctx, r.db, record.Key(), record.Scope().Service())
	if err != nil {
		return fmt.Errorf("sql: verify commit miss: %w", err)
	}
	if existing == nil {
		return model.ErrInvalidState
	}
	if !existing.Owner().Equals(record.Owner()) {
		return model.ErrOwnerMismatch
	}
	if existing.ConflictsWith(record.Fingerprint()) {
		return model.ErrFingerprintConflict
	}
	if existing.Status() == record.Status() {
		return nil
	}
	return model.ErrInvalidState
}

// ---- Record mapping ----

type sqlRecord struct {
	Key, Fingerprint, Owner, Operation   string
	ScopeService, ScopeTenant, ScopeUser string
	Status                               string
	StatusCode                           int
	RespHeaders, RespBody, RespCodec     string
	ErrorCode, ErrorMessage              string
	CreatedAt, UpdatedAt, ExpiresAt      time.Time
}

func (r *sqlRecord) toDomain() (*model.IdempotencyRecord, error) {
	var headers map[string][]string
	if r.RespHeaders != "" {
		if err := json.Unmarshal([]byte(r.RespHeaders), &headers); err != nil {
			return nil, fmt.Errorf("sql: unmarshal resp_headers: %w", err)
		}
	}
	resp := model.CapturedResponse{
		StatusCode: r.StatusCode,
		Headers:    headers,
		Body:       []byte(r.RespBody),
		Codec:      r.RespCodec,
	}
	return model.RestoreRecord(model.RestoreRecordParams{
		Key:          valueobject.UnsafeIdempotencyKey(r.Key),
		Fingerprint:  valueobject.UnsafeFingerprint(r.Fingerprint),
		Owner:        valueobject.UnsafeOwner(r.Owner),
		Operation:    valueobject.UnsafeOperation(r.Operation),
		Scope:        valueobject.NewScope(r.ScopeService, r.ScopeTenant, r.ScopeUser),
		Status:       model.IdempotencyStatus(r.Status),
		Response:     resp,
		ErrorCode:    r.ErrorCode,
		ErrorMessage: r.ErrorMessage,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		ExpiresAt:    r.ExpiresAt,
	}), nil
}
