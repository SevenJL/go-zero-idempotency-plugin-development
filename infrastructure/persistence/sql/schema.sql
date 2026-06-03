-- =============================================================================
-- Idempotency record storage schema — MySQL / PostgreSQL compatible
--
-- This schema is designed for idempotency-key deduplication using a relational
-- database. The unique constraint on (scope_service, idempotency_key) enforces
-- at-most-once semantics per service scope.
--
-- MySQL:
--   mysql -u root < schema.sql
--
-- PostgreSQL:
--   psql -f schema.sql
-- =============================================================================

-- MySQL variant
CREATE TABLE IF NOT EXISTS idempotency_records (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    idempotency_key VARCHAR(255) NOT NULL,
    fingerprint     VARCHAR(128) NOT NULL,
    owner           VARCHAR(64)  NOT NULL,
    operation       VARCHAR(255) NOT NULL DEFAULT '',
    scope_service   VARCHAR(128) NOT NULL DEFAULT '',
    scope_tenant    VARCHAR(128) NOT NULL DEFAULT '',
    scope_user      VARCHAR(128) NOT NULL DEFAULT '',
    status          VARCHAR(20)  NOT NULL DEFAULT 'processing',
    status_code     INT          NOT NULL DEFAULT 0,
    resp_headers    MEDIUMTEXT,
    resp_body       MEDIUMBLOB,
    resp_codec      VARCHAR(50)  NOT NULL DEFAULT '',
    error_code      VARCHAR(50)  NOT NULL DEFAULT '',
    error_message   VARCHAR(1024) NOT NULL DEFAULT '',
    created_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at      DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    expires_at      DATETIME(3)  NOT NULL,

    UNIQUE INDEX uq_key_scope (scope_service, idempotency_key),
    INDEX idx_status (status),
    INDEX idx_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Cleanup expired records (run as a scheduled job or cron):
-- DELETE FROM idempotency_records WHERE expires_at < NOW();

-- PostgreSQL variant (uncomment to use):
-- CREATE TABLE IF NOT EXISTS idempotency_records (
--     id              BIGSERIAL PRIMARY KEY,
--     idempotency_key VARCHAR(255) NOT NULL,
--     fingerprint     VARCHAR(128) NOT NULL,
--     owner           VARCHAR(64)  NOT NULL,
--     operation       VARCHAR(255) NOT NULL DEFAULT '',
--     scope_service   VARCHAR(128) NOT NULL DEFAULT '',
--     scope_tenant    VARCHAR(128) NOT NULL DEFAULT '',
--     scope_user      VARCHAR(128) NOT NULL DEFAULT '',
--     status          VARCHAR(20)  NOT NULL DEFAULT 'processing',
--     status_code     INT          NOT NULL DEFAULT 0,
--     resp_headers    TEXT,
--     resp_body       BYTEA,
--     resp_codec      VARCHAR(50)  NOT NULL DEFAULT '',
--     error_code      VARCHAR(50)  NOT NULL DEFAULT '',
--     error_message   VARCHAR(1024) NOT NULL DEFAULT '',
--     created_at      TIMESTAMP(3) NOT NULL DEFAULT NOW(),
--     updated_at      TIMESTAMP(3) NOT NULL DEFAULT NOW(),
--     expires_at      TIMESTAMP(3) NOT NULL,
--
--     CONSTRAINT uq_key_scope UNIQUE (scope_service, idempotency_key)
-- );
-- CREATE INDEX idx_status ON idempotency_records (status);
-- CREATE INDEX idx_expires_at ON idempotency_records (expires_at);
