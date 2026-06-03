-- =============================================================================
-- Idempotency record storage schema
--
-- This schema enforces at-most-once semantics per (scope_service, idempotency_key)
-- via a UNIQUE constraint. Supports both MySQL (5.7+) and PostgreSQL (12+).
--
-- Apply with:
--   MySQL:      mysql -u root -p <database> < schema.sql
--   PostgreSQL: psql -h <host> -U <user> -d <db> -f schema.sql
-- =============================================================================

-- ---------------------------------------------------------------------------
-- MySQL
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS idempotency_records (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    idempotency_key VARCHAR(128)  NOT NULL,
    fingerprint     VARCHAR(128)  NOT NULL,
    owner           VARCHAR(64)   NOT NULL,
    operation       VARCHAR(256)  NOT NULL DEFAULT '',
    scope_service   VARCHAR(128)  NOT NULL DEFAULT '',
    scope_tenant    VARCHAR(128)  NOT NULL DEFAULT '',
    scope_user      VARCHAR(128)  NOT NULL DEFAULT '',
    status          VARCHAR(20)   NOT NULL DEFAULT 'processing',
    status_code     INT           NOT NULL DEFAULT 0,
    resp_headers    MEDIUMTEXT,
    resp_body       MEDIUMTEXT,
    resp_codec      VARCHAR(64)   NOT NULL DEFAULT 'application/json',
    error_code      VARCHAR(64)   NOT NULL DEFAULT '',
    error_message   TEXT,
    created_at      DATETIME(3)   NOT NULL,
    updated_at      DATETIME(3)   NOT NULL,
    expires_at      DATETIME(3)   NOT NULL,

    UNIQUE INDEX uq_key_scope (scope_service, idempotency_key),
    INDEX idx_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------------
-- PostgreSQL (uncomment to use)
-- ---------------------------------------------------------------------------
-- CREATE TABLE IF NOT EXISTS idempotency_records (
--     id              BIGSERIAL PRIMARY KEY,
--     idempotency_key VARCHAR(128)  NOT NULL,
--     fingerprint     VARCHAR(128)  NOT NULL,
--     owner           VARCHAR(64)   NOT NULL,
--     operation       VARCHAR(256)  NOT NULL DEFAULT '',
--     scope_service   VARCHAR(128)  NOT NULL DEFAULT '',
--     scope_tenant    VARCHAR(128)  NOT NULL DEFAULT '',
--     scope_user      VARCHAR(128)  NOT NULL DEFAULT '',
--     status          VARCHAR(20)   NOT NULL DEFAULT 'processing',
--     status_code     INT           NOT NULL DEFAULT 0,
--     resp_headers    TEXT,
--     resp_body       TEXT,
--     resp_codec      VARCHAR(64)   NOT NULL DEFAULT 'application/json',
--     error_code      VARCHAR(64)   NOT NULL DEFAULT '',
--     error_message   TEXT,
--     created_at      TIMESTAMP(3)  NOT NULL,
--     updated_at      TIMESTAMP(3)  NOT NULL,
--     expires_at      TIMESTAMP(3)  NOT NULL,
--
--     CONSTRAINT uq_key_scope UNIQUE (scope_service, idempotency_key)
-- );
-- CREATE INDEX IF NOT EXISTS idx_expires_at ON idempotency_records (expires_at);
