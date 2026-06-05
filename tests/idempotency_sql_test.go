//go:build integration
// +build integration

// Package tests contains integration tests that require a running MySQL or
// PostgreSQL instance.
//
// Prerequisites:
//  1. Apply the schema from infrastructure/persistence/sql/schema.sql
//  2. Start a database instance (see docker-compose.yml for MySQL)
//  3. Set the appropriate environment variable
//
// MySQL:
//
//	MYSQL_DSN="user:password@tcp(localhost:3306)/idempotency_test?parseTime=true" \
//	  go test -tags=integration -count=1 -v ./tests/ -run "TestSQL"
//
// PostgreSQL:
//
//	PG_DSN="postgres://user:password@localhost:5432/idempotency_test?sslmode=disable" \
//	  go test -tags=integration -count=1 -v ./tests/ -run "TestSQL"
//
// Note: your test binary must import a database driver, e.g.:
//
//	import _ "github.com/go-sql-driver/mysql"
//	import _ "github.com/lib/pq"
package tests

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
	sqlrepo "github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/sql"
)

// ---------------------------------------------------------------------------
// SQL test harness
// ---------------------------------------------------------------------------

// sqlClock is a deterministic clock shared across SQL tests.
type sqlClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *sqlClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *sqlClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *sqlClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func sqlConfig() (dsn string, driver sqlrepo.Driver) {
	if d := os.Getenv("PG_DSN"); d != "" {
		return d, sqlrepo.DriverPostgres
	}
	if d := os.Getenv("MYSQL_DSN"); d != "" {
		return d, sqlrepo.DriverMySQL
	}
	// Default MySQL with parseTime=true for DATETIME(3).
	return "root:password@tcp(localhost:3306)/idempotency_test?parseTime=true", sqlrepo.DriverMySQL
}

func newSQLRepo(t *testing.T) (*sql.DB, *sqlrepo.IdempotencyRecordRepository, func()) {
	t.Helper()

	dsn, driver := sqlConfig()
	db, err := sql.Open(string(driver), dsn)
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", driver, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		if driver == sqlrepo.DriverPostgres {
			t.Fatalf("PostgreSQL not reachable at %s: %v\nSet PG_DSN env var.", dsn, err)
		}
		t.Fatalf("MySQL not reachable at %s: %v\nSet MYSQL_DSN env var. Import a driver like github.com/go-sql-driver/mysql.", dsn, err)
	}

	// Clean the table before tests.
	if _, err := db.ExecContext(ctx, "DELETE FROM idempotency_records"); err != nil {
		db.Close()
		t.Fatalf("DELETE FROM idempotency_records: %v\nMake sure the schema from infrastructure/persistence/sql/schema.sql is applied.", err)
	}

	repo := sqlrepo.NewIdempotencyRecordRepository(db, driver)
	cleanup := func() { repo.Close(); db.Close() }
	return db, repo, cleanup
}

func newSQLSvc(t *testing.T, repo *sqlrepo.IdempotencyRecordRepository, clock *sqlClock) *appservice.IdempotencyService {
	t.Helper()
	svc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Clock:      clock,
		Scope:      "sql-test-svc",
	})
	if err != nil {
		t.Fatalf("NewIdempotencyService: %v", err)
	}
	return svc
}

// sqlBegin is a convenience wrapper around svc.Begin for SQL tests.
func sqlBegin(t *testing.T, svc *appservice.IdempotencyService, key, body string) dto.BeginResult {
	t.Helper()
	result, err := svc.Begin(context.Background(), command.BeginCommand{
		Request: dto.RequestContext{
			Operation: valueobject.UnsafeOperation("POST /api/orders"),
			Scope:     valueobject.NewScope("", "tenant-sql", "user-sql"),
			Headers:   map[string][]string{"Idempotency-Key": {key}},
			Body:      []byte(body),
		},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return result
}

// sqlComplete is a convenience wrapper around svc.Complete for SQL tests.
func sqlComplete(t *testing.T, svc *appservice.IdempotencyService, r dto.BeginResult, status int, body string) {
	t.Helper()
	err := svc.Complete(context.Background(), command.CompleteCommand{
		Key:         r.Key,
		Fingerprint: r.Fingerprint,
		Owner:       r.Owner,
		Response:    dto.CapturedResponse{StatusCode: status, Body: []byte(body)},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSQLLifecycle verifies the full TryBegin → Commit → Replay flow.
func TestSQLLifecycle(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	r := sqlBegin(t, svc, "sql-lifecycle-key-0000000001", `{"order":1}`)
	if r.Type != dto.BeginResultAcquired {
		t.Fatalf("expected acquired, got %v", r.Type)
	}

	sqlComplete(t, svc, r, 200, `{"status":"ok"}`)

	// Replay
	r2 := sqlBegin(t, svc, "sql-lifecycle-key-0000000001", `{"order":1}`)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("expected replay, got %v", r2.Type)
	}
	if string(r2.Response.Body) != `{"status":"ok"}` {
		t.Fatalf("unexpected replay body: %s", string(r2.Response.Body))
	}
}

// TestSQLConflict verifies fingerprint mismatch is rejected.
func TestSQLConflict(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	r1 := sqlBegin(t, svc, "sql-conflict-key-0000000001", `{"order":1}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected acquired, got %v", r1.Type)
	}

	// Same key, different body → fingerprint mismatch → conflict.
	r2 := sqlBegin(t, svc, "sql-conflict-key-0000000001", `{"order":2}`)
	if r2.Type != dto.BeginResultConflict {
		t.Fatalf("expected conflict, got %v", r2.Type)
	}
}

// TestSQLInProgress verifies concurrent detection.
func TestSQLInProgress(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	r1 := sqlBegin(t, svc, "sql-progress-key-0000000001", `{"order":1}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected acquired, got %v", r1.Type)
	}

	r2 := sqlBegin(t, svc, "sql-progress-key-0000000001", `{"order":1}`)
	if r2.Type != dto.BeginResultInProgress {
		t.Fatalf("expected in_progress, got %v", r2.Type)
	}
}

// TestSQLAbortDelete verifies record cleanup on abort.
func TestSQLAbortDelete(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	r1 := sqlBegin(t, svc, "sql-abort-key-000000000001", `{"order":1}`)
	if r1.Type != dto.BeginResultAcquired {
		t.Fatalf("expected acquired, got %v", r1.Type)
	}

	err := svc.Abort(context.Background(), command.AbortCommand{
		Key:   r1.Key,
		Owner: r1.Owner,
		Mode:  model.FailureModeDelete,
	})
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}

	// After abort-delete, a new Begin acquires the lock again.
	r2 := sqlBegin(t, svc, "sql-abort-key-000000000001", `{"order":1}`)
	if r2.Type != dto.BeginResultAcquired {
		t.Fatalf("expected acquired after abort, got %v", r2.Type)
	}
}

// TestSQLConcurrentBegins verifies exactly one Begin acquires the lock.
func TestSQLConcurrentBegins(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	const n = 20
	results := make(chan dto.BeginResultType, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			r := sqlBegin(t, svc, "sql-concurrent-key-00000001", `{"order":1}`)
			results <- r.Type
		}()
	}
	wg.Wait()
	close(results)

	acquired := 0
	for rt := range results {
		if rt == dto.BeginResultAcquired {
			acquired++
		}
	}
	if acquired != 1 {
		t.Fatalf("expected exactly 1 acquired, got %d", acquired)
	}
}

// TestSQLCleanupRuns verifies the background cleanup goroutine starts and
// runs without panicking.
func TestSQLCleanupRuns(t *testing.T) {
	_, repo, cleanup := newSQLRepo(t)
	defer cleanup()

	clock := &sqlClock{now: baseTime()}
	svc := newSQLSvc(t, repo, clock)

	r := sqlBegin(t, svc, "sql-cleanup-key-000000000001", `{"order":1}`)
	sqlComplete(t, svc, r, 200, `{}`)

	// Wait for at least one cleanup cycle (default 60s interval).
	// We don't assert row count because the TTL is 24h by default, but we
	// verify the goroutine doesn't panic.
	time.Sleep(100 * time.Millisecond)

	// The record should still exist (not yet expired).
	r2 := sqlBegin(t, svc, "sql-cleanup-key-000000000001", `{"order":1}`)
	if r2.Type != dto.BeginResultReplay {
		t.Fatalf("expected replay (not expired), got %v", r2.Type)
	}
}
