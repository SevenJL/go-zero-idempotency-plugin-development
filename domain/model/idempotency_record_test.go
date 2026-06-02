package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

func TestIdempotencyRecordComplete(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	key := valueobject.UnsafeIdempotencyKey("test-key-123456789")
	fp := valueobject.UnsafeFingerprint("sha256:first")
	owner := valueobject.UnsafeOwner("owner-1")
	operation := valueobject.UnsafeOperation("POST /orders")

	record, err := model.NewProcessingRecord(model.NewRecordParams{
		Key:           key,
		Fingerprint:   fp,
		Owner:         owner,
		Operation:     operation,
		Now:           now,
		ProcessingTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewProcessingRecord() error = %v", err)
	}

	response := model.CapturedResponse{StatusCode: 201, Body: []byte(`{"id":"1"}`)}
	if err := record.Complete(owner, fp, response, now.Add(time.Second), time.Hour); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if record.Status() != model.StatusCompleted {
		t.Fatalf("Status() = %s, want %s", record.Status(), model.StatusCompleted)
	}
	if string(record.Response().Body) != `{"id":"1"}` {
		t.Fatalf("Response().Body = %q", string(record.Response().Body))
	}
}

func TestIdempotencyRecordCompleteRejectsOwnerMismatch(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	record, err := model.NewProcessingRecord(model.NewRecordParams{
		Key:           valueobject.UnsafeIdempotencyKey("test-key-123456789"),
		Fingerprint:   valueobject.UnsafeFingerprint("sha256:first"),
		Owner:         valueobject.UnsafeOwner("owner-1"),
		Operation:     valueobject.UnsafeOperation("POST /orders"),
		Now:           now,
		ProcessingTTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewProcessingRecord() error = %v", err)
	}

	err = record.Complete(
		valueobject.UnsafeOwner("owner-2"),
		valueobject.UnsafeFingerprint("sha256:first"),
		model.CapturedResponse{StatusCode: 200, Body: []byte("{}")},
		now,
		time.Hour,
	)
	if !errors.Is(err, model.ErrOwnerMismatch) {
		t.Fatalf("Complete() error = %v, want %v", err, model.ErrOwnerMismatch)
	}
}
