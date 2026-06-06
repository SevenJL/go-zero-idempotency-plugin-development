package sql

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

func TestTryBeginDuplicateReturnsExistingDecision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewIdempotencyRecordRepositoryWithCleanup(db, DriverMySQL, 0)
	record := newSQLTestRecord(t, "dup-key-000000000001", "svc-a", "fp-a", model.StatusProcessing)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO idempotency_records")).
		WithArgs(insertArgs(record, false)...).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectQuery(regexp.QuoteMeta("FROM idempotency_records")).
		WithArgs(record.Key().String(), record.Scope().Service()).
		WillReturnRows(rowsFor(record))
	mock.ExpectCommit()

	decision, err := repo.TryBegin(context.Background(), record)
	if err != nil {
		t.Fatalf("TryBegin: %v", err)
	}
	if decision.Type != model.DecisionInProgress {
		t.Fatalf("decision type = %s, want %s", decision.Type, model.DecisionInProgress)
	}
	if decision.Record == nil || decision.Record.Key().String() != record.Key().String() {
		t.Fatalf("decision record = %#v, want duplicate record", decision.Record)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestTryBeginPostgresDuplicateUsesScopedPlaceholderQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewIdempotencyRecordRepositoryWithCleanup(db, DriverPostgres, 0)
	record := newSQLTestRecord(t, "pg-key-0000000000001", "svc-pg", "fp-pg", model.StatusProcessing)
	completed := record.Clone()
	if err := completed.Complete(record.Owner(), record.Fingerprint(), model.CapturedResponse{
		StatusCode: 200,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"ok":true}`),
		Codec:      "application/json",
	}, record.UpdatedAt().Add(time.Second), time.Hour); err != nil {
		t.Fatalf("Complete test record: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO idempotency_records")).
		WithArgs(insertArgs(record, true)...).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE idempotency_key = $1 AND scope_service = $2 AND expires_at > NOW()")).
		WithArgs(record.Key().String(), record.Scope().Service()).
		WillReturnRows(rowsFor(completed))
	mock.ExpectCommit()

	decision, err := repo.TryBegin(context.Background(), record)
	if err != nil {
		t.Fatalf("TryBegin: %v", err)
	}
	if decision.Type != model.DecisionReplay {
		t.Fatalf("decision type = %s, want %s", decision.Type, model.DecisionReplay)
	}
	if string(decision.Record.Response().Body) != `{"ok":true}` {
		t.Fatalf("replay body = %q", string(decision.Record.Response().Body))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCommitMissMapsOwnerMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewIdempotencyRecordRepositoryWithCleanup(db, DriverMySQL, 0)
	record := newSQLTestRecord(t, "commit-key-0000000001", "svc-a", "fp-a", model.StatusProcessing)
	completed := record.Clone()
	if err := completed.Complete(record.Owner(), record.Fingerprint(), model.CapturedResponse{
		StatusCode: 200,
		Body:       []byte(`{"ok":true}`),
	}, record.UpdatedAt().Add(time.Second), time.Hour); err != nil {
		t.Fatalf("Complete test record: %v", err)
	}

	existing := record.Clone()
	existingOwner := valueobject.UnsafeOwner("other-owner")
	existing = model.RestoreRecord(model.RestoreRecordParams{
		Key:          existing.Key(),
		Fingerprint:  existing.Fingerprint(),
		Owner:        existingOwner,
		Operation:    existing.Operation(),
		Scope:        existing.Scope(),
		Status:       existing.Status(),
		Response:     existing.Response(),
		ErrorCode:    existing.ErrorCode(),
		ErrorMessage: existing.ErrorMessage(),
		CreatedAt:    existing.CreatedAt(),
		UpdatedAt:    existing.UpdatedAt(),
		ExpiresAt:    existing.ExpiresAt(),
	})

	mock.ExpectExec(regexp.QuoteMeta("UPDATE idempotency_records")).
		WithArgs(commitArgs(completed)...).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM idempotency_records")).
		WithArgs(completed.Key().String(), completed.Scope().Service()).
		WillReturnRows(rowsFor(existing))

	err = repo.Commit(context.Background(), completed)
	if !errors.Is(err, model.ErrOwnerMismatch) {
		t.Fatalf("Commit error = %v, want owner mismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestQueryBuildersUseDriverPlaceholders(t *testing.T) {
	mysqlRepo := NewIdempotencyRecordRepositoryWithCleanup(nil, DriverMySQL, 0)
	postgresRepo := NewIdempotencyRecordRepositoryWithCleanup(nil, DriverPostgres, 0)

	if strings.Contains(mysqlRepo.findQuery(true), "$1") {
		t.Fatalf("mysql find query contains postgres placeholders: %s", mysqlRepo.findQuery(true))
	}
	if !strings.Contains(postgresRepo.findQuery(true), "$1") || !strings.Contains(postgresRepo.updateQuery(), "$13") {
		t.Fatalf("postgres queries should use numbered placeholders")
	}
	if !strings.Contains(postgresRepo.deleteByOwnerQuery(), "$3") {
		t.Fatalf("postgres delete query should use numbered placeholders: %s", postgresRepo.deleteByOwnerQuery())
	}
}

func newSQLTestRecord(t *testing.T, key, scope, fp string, status model.IdempotencyStatus) *model.IdempotencyRecord {
	t.Helper()

	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	record, err := model.NewProcessingRecord(model.NewRecordParams{
		Key:           valueobject.UnsafeIdempotencyKey(key),
		Fingerprint:   valueobject.UnsafeFingerprint("sha256:" + fp),
		Owner:         valueobject.UnsafeOwner("owner-1"),
		Operation:     valueobject.UnsafeOperation("POST /orders"),
		Scope:         valueobject.NewScope(scope, "tenant-1", "user-1"),
		Now:           now,
		ProcessingTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewProcessingRecord: %v", err)
	}
	if status == model.StatusProcessing {
		return record
	}
	if status == model.StatusCompleted {
		if err := record.Complete(record.Owner(), record.Fingerprint(), model.CapturedResponse{
			StatusCode: 200,
			Body:       []byte(`{"ok":true}`),
		}, now.Add(time.Second), time.Hour); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		return record
	}
	t.Fatalf("unsupported test status: %s", status)
	return nil
}

func rowsFor(record *model.IdempotencyRecord) *sqlmock.Rows {
	resp := record.Response()
	headers, _ := jsonMarshalString(resp.Headers)

	return sqlmock.NewRows([]string{
		"idempotency_key", "fingerprint", "owner", "operation",
		"scope_service", "scope_tenant", "scope_user",
		"status", "status_code", "resp_headers", "resp_body", "resp_codec",
		"error_code", "error_message", "created_at", "updated_at", "expires_at",
	}).AddRow(
		record.Key().String(), record.Fingerprint().String(), record.Owner().String(), record.Operation().String(),
		record.Scope().Service(), record.Scope().Tenant(), record.Scope().User(),
		record.Status().String(), resp.StatusCode, headers, string(resp.Body), resp.Codec,
		record.ErrorCode(), record.ErrorMessage(), record.CreatedAt(), record.UpdatedAt(), record.ExpiresAt(),
	)
}

func insertArgs(record *model.IdempotencyRecord, postgres bool) []driver.Value {
	resp := record.Response()
	args := []driver.Value{
		record.Key().String(),
		record.Fingerprint().String(),
		record.Owner().String(),
		record.Operation().String(),
		record.Scope().Service(),
		record.Scope().Tenant(),
		record.Scope().User(),
		record.Status().String(),
		"{}",
		string(resp.Body),
		resp.Codec,
		record.ExpiresAt().Format("2006-01-02 15:04:05.000"),
	}
	if postgres {
		args = append(args, record.CreatedAt().Format("2006-01-02 15:04:05.000"))
	}
	return args
}

func commitArgs(record *model.IdempotencyRecord) []driver.Value {
	resp := record.Response()
	return []driver.Value{
		record.Status().String(),
		resp.StatusCode,
		"{}",
		string(resp.Body),
		resp.Codec,
		record.ErrorCode(),
		record.ErrorMessage(),
		record.ExpiresAt().Format("2006-01-02 15:04:05.000"),
		record.Key().String(),
		record.Scope().Service(),
		record.Owner().String(),
		record.Fingerprint().String(),
		model.StatusProcessing.String(),
	}
}

func jsonMarshalString(v any) (string, error) {
	if v == nil {
		return "null", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
