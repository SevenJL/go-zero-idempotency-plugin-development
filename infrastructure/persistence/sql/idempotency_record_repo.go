// Package sql provides an IdempotencyRecordRepository backed by a relational
// database (MySQL or PostgreSQL). It uses INSERT ... ON DUPLICATE KEY (MySQL)
// or INSERT ... ON CONFLICT (PostgreSQL) for atomic TryBegin semantics.
//
// Usage:
//
//	db, _ := sql.Open("mysql", dsn)
//	repo := sqlrepo.NewIdempotencyRecordRepository(db, sqlrepo.DriverMySQL)
//	svc, _ := appservice.NewIdempotencyService(appservice.Config{Repository: repo, ...})
//
// For PostgreSQL:
//
//	repo := sqlrepo.NewIdempotencyRecordRepository(db, sqlrepo.DriverPostgres)
//
// The repository requires the schema defined in schema.sql to be applied first.
//
// Expired record cleanup runs in a background goroutine at a configurable
// interval (default: 60s). Call Close() to stop the cleanup goroutine during
// graceful shutdown.
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

	err = r.insertRecordTx(ctx, tx, record, string(headersJSON))
	if err == nil {
		if commitErr := tx.Commit(); commitErr != nil {
			return model.BeginDecision{}, fmt.Errorf("sql: commit tx: %w", commitErr)
		}
		return model.Acquired(record), nil
	}

	existing, findErr := r.FindTx(ctx, tx, record.Key())
	if findErr != nil || existing == nil {
		return model.BeginDecision{}, fmt.Errorf("sql: duplicate key but record not found: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return model.BeginDecision{}, fmt.Errorf("sql: commit tx: %w", err)
	}

	if existing.Fingerprint().String() != record.Fingerprint().String() {
		return model.Conflict(existing), nil
	}

	switch existing.Status() {
	case model.StatusProcessing:
		return model.InProgress(existing), nil
	case model.StatusCompleted:
		return model.Replay(existing), nil
	case model.StatusFailed:
		return model.Failed(existing), nil
	default:
		return model.InProgress(existing), nil
	}
}

func (r *IdempotencyRecordRepository) Commit(ctx context.Context, record *model.IdempotencyRecord) error {
	headersJSON, _ := json.Marshal(record.Response().Headers)
	query := r.updateQuery()

	resp := record.Response()
	expiresAt := record.ExpiresAt().Format("2006-01-02 15:04:05.000")

	result, err := r.db.ExecContext(ctx, query,
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
	n, _ := result.RowsAffected()
	if n == 0 {
		// RowsAffected may be 0 when the row matched but had identical
		// values (a MySQL driver optimisation). Verify by re-reading —
		// if the record exists with the expected status, the commit is
		// effectively a no-op (idempotent retry) and is not an error.
		existing, findErr := r.Find(ctx, record.Key())
		if findErr != nil || existing == nil {
			return model.ErrOwnerMismatch
		}
		if existing.Status() == record.Status() && existing.Owner().Equals(record.Owner()) {
			return nil
		}
		return model.ErrOwnerMismatch
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
		return nil // record already cleaned up
	}
	scopeService := existing.Scope().Service()

	switch mode {
	case model.FailureModeDelete:
		_, err := tx.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE scope_service = ? AND idempotency_key = ? AND owner = ?`,
			scopeService, key.String(), owner.String())
		if err != nil {
			return fmt.Errorf("sql: abort delete: %w", err)
		}
	case model.FailureModeCache:
		_, err := tx.ExecContext(ctx,
			`UPDATE idempotency_records SET status = 'failed', updated_at = NOW(3) WHERE scope_service = ? AND idempotency_key = ? AND owner = ?`,
			scopeService, key.String(), owner.String())
		if err != nil {
			return fmt.Errorf("sql: abort cache: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sql: abort commit: %w", err)
	}
	return nil
}

func (r *IdempotencyRecordRepository) Find(ctx context.Context, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT idempotency_key, fingerprint, owner, operation, scope_service, scope_tenant, scope_user,
		        status, status_code, resp_headers, resp_body, resp_codec, error_code, error_message,
		        created_at, updated_at, expires_at
		 FROM idempotency_records
		 WHERE idempotency_key = ? AND expires_at > NOW()
		 ORDER BY created_at DESC LIMIT 1`,
		key.String(),
	)

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

func (r *IdempotencyRecordRepository) Renew(ctx context.Context, key valueobject.IdempotencyKey, owner valueobject.Owner, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl).Format("2006-01-02 15:04:05.000")
	_, err := r.db.ExecContext(ctx,
		`UPDATE idempotency_records SET expires_at = ? WHERE idempotency_key = ? AND owner = ? AND status = 'processing'`,
		expiresAt, key.String(), owner.String(),
	)
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

func (r *IdempotencyRecordRepository) insertRecord(ctx context.Context, record *model.IdempotencyRecord, headersJSON string) error {
	return r.insertRecordTx(ctx, r.db, record, headersJSON)
}

func (r *IdempotencyRecordRepository) insertRecordTx(ctx context.Context, exec execContext, record *model.IdempotencyRecord, headersJSON string) error {
	resp := record.Response()
	expiresAt := record.ExpiresAt().Format("2006-01-02 15:04:05.000")
	now := record.CreatedAt().Format("2006-01-02 15:04:05.000")

	if r.driver == DriverPostgres {
		_, err := exec.ExecContext(ctx, r.insertQuery(),
			record.Key().String(), record.Fingerprint().String(), record.Owner().String(),
			record.Operation().String(), record.Scope().Service(), record.Scope().Tenant(), record.Scope().User(),
			record.Status().String(), headersJSON, string(resp.Body), resp.Codec,
			expiresAt, now,
		)
		return err
	}
	_, err := exec.ExecContext(ctx, r.insertQuery(),
		record.Key().String(), record.Fingerprint().String(), record.Owner().String(),
		record.Operation().String(), record.Scope().Service(), record.Scope().Tenant(), record.Scope().User(),
		record.Status().String(), headersJSON, string(resp.Body), resp.Codec,
		expiresAt,
	)
	return err
}

// execContext abstracts *sql.DB and *sql.Tx for queries.
type execContext interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// FindTx reads a record within a transaction.
func (r *IdempotencyRecordRepository) FindTx(ctx context.Context, exec execContext, key valueobject.IdempotencyKey) (*model.IdempotencyRecord, error) {
	row := exec.QueryRowContext(ctx,
		`SELECT idempotency_key, fingerprint, owner, operation, scope_service, scope_tenant, scope_user,
		        status, status_code, resp_headers, resp_body, resp_codec, error_code, error_message,
		        created_at, updated_at, expires_at
		 FROM idempotency_records
		 WHERE idempotency_key = ? AND expires_at > NOW()
		 ORDER BY created_at DESC LIMIT 1`,
		key.String(),
	)

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

func (r *IdempotencyRecordRepository) deleteExpired(ctx context.Context) {
	r.db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE expires_at < NOW()`)
}

// ---- Record mapping ----

type sqlRecord struct {
	Key, Fingerprint, Owner, Operation                   string
	ScopeService, ScopeTenant, ScopeUser                 string
	Status                                               string
	StatusCode                                           int
	RespHeaders, RespBody, RespCodec                     string
	ErrorCode, ErrorMessage                              string
	CreatedAt, UpdatedAt, ExpiresAt                      time.Time
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
