// Package service provides YAML-configurable idempotency settings.
//
// ConfigFile is the YAML-friendly configuration struct that maps to
// the idempotency section of a go-zero service configuration file.
// It is designed to be embedded into the service's own config struct:
//
//	type MyConfig struct {
//	    rest.RestConf
//	    Idempotency service.ConfigFile `json:",optional" yaml:",optional"`
//	}
//
// Usage:
//
//	var cfg MyConfig
//	conf.MustLoad("etc/config.yaml", &cfg)
//	idemCfg, err := cfg.Idempotency.ToServiceConfig(repo, logger, metrics, tracer)
//	idemSvc, _ := appservice.NewIdempotencyService(idemCfg)
package service

import (
	"fmt"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/repository"
	domainservice "github.com/sevenjl/go-zero-idempotency-plugin-development/domain/service"
)

// ConfigFile is the YAML-deserializable idempotency configuration.
// All fields are optional; omitted fields use safe production defaults.
// Call ToServiceConfig to convert to a runtime Config.
type ConfigFile struct {
	// Disabled explicitly turns off the middleware (default: false = enabled).
	Disabled bool `json:",optional" yaml:",optional"`

	// Scope is the service identifier used in fingerprint calculation.
	Scope string `json:",optional" yaml:",optional"`

	// Key configures idempotency-key extraction.
	Key KeyConfig `json:",optional" yaml:",optional"`

	// Fingerprint configures the request fingerprint strategy.
	Fingerprint FingerprintConfig `json:",optional" yaml:",optional"`

	// ProcessingTTL is the maximum time a request may spend in the
	// "processing" state before the record expires (default: 30s).
	ProcessingTTL time.Duration `json:",optional" yaml:",optional,default=30s"`

	// CompletedTTL is how long a completed record is kept for replay
	// (default: 24h).
	CompletedTTL time.Duration `json:",optional" yaml:",optional,default=24h"`

	// FailedTTL is how long a failed record is cached before allowing
	// a retry (default: 5m). Only meaningful when FailureMode is "cache".
	FailedTTL time.Duration `json:",optional" yaml:",optional,default=5m"`

	// DuplicatePolicy controls what happens when a duplicate request
	// arrives while the first is still processing.
	// Values: "reject" (default), "wait", "pass_through".
	DuplicatePolicy string `json:",optional" yaml:",optional,default=reject"`

	// FailureMode controls what happens to the idempotency record when
	// the business handler fails.
	// Values: "delete" (default), "cache", "keep_processing_until_ttl".
	FailureMode string `json:",optional" yaml:",optional,default=delete"`

	// StorageFailureMode controls behaviour when the storage backend
	// (e.g. Redis) is unavailable.
	// Values: "fail_closed" (default), "fail_open".
	StorageFailureMode string `json:",optional" yaml:",optional,default=fail_closed"`

	// WaitTimeout is the maximum time a duplicate request will wait for
	// the original request to complete when DuplicatePolicy is "wait"
	// (default: 5s).
	WaitTimeout time.Duration `json:",optional" yaml:",optional,default=5s"`

	// WaitInterval is the polling interval between checks when
	// DuplicatePolicy is "wait" (default: 50ms).
	WaitInterval time.Duration `json:",optional" yaml:",optional,default=50ms"`

	// Capture rules for response caching (see CaptureConfig).
	Capture CaptureConfig `json:",optional" yaml:",optional"`
}

// KeyConfig controls how the idempotency key is extracted from requests.
type KeyConfig struct {
	// HeaderName is the HTTP header or gRPC metadata key that carries
	// the idempotency key (default: "Idempotency-Key").
	HeaderName string `json:",optional" yaml:",optional,default=Idempotency-Key"`

	// Required makes the idempotency key mandatory. When true, requests
	// without a valid key are rejected with 400 (HTTP) or InvalidArgument
	// (gRPC). When false, they pass through unprotected (default: true).
	// Use *bool to distinguish between "not set" (nil → default true) and
	// "explicitly set to false" (pointer to false).
	Required *bool `json:",optional" yaml:",optional"`

	// MinLength is the minimum allowed key length (default: 16).
	MinLength int `json:",optional" yaml:",optional,default=16"`

	// MaxLength is the maximum allowed key length (default: 128).
	MaxLength int `json:",optional" yaml:",optional,default=128"`
}

// FingerprintConfig controls how request fingerprints are computed.
type FingerprintConfig struct {
	// IncludeTenant adds the tenant ID to the fingerprint (default: true).
	IncludeTenant *bool `json:",optional" yaml:",optional"`

	// IncludeUser adds the user ID to the fingerprint (default: true).
	IncludeUser *bool `json:",optional" yaml:",optional"`

	// IncludeBody adds the canonical request body to the fingerprint
	// (default: true).
	IncludeBody *bool `json:",optional" yaml:",optional"`

	// MaxBodyBytes limits the portion of the request body that is hashed
	// into the fingerprint (default: 1 MB).
	MaxBodyBytes int64 `json:",optional" yaml:",optional,default=1048576"`
}

// CaptureConfig controls which responses are eligible for caching and replay.
type CaptureConfig struct {
	// MaxBodyBytes is the maximum response body size to cache (default: 1 MB).
	MaxBodyBytes int64 `json:",optional" yaml:",optional,default=1048576"`

	// ContentTypes is the allowed list of content-type prefixes
	// (default: ["application/json"]).
	ContentTypes []string `json:",optional" yaml:",optional"`

	// ExcludedHeaders lists response headers that must never be stored
	// (default: ["Set-Cookie","Authorization","Cookie","WWW-Authenticate"]).
	ExcludedHeaders []string `json:",optional" yaml:",optional"`

	// CacheStatus2xx enables caching of successful responses (default: true).
	CacheStatus2xx *bool `json:",optional" yaml:",optional"`

	// CacheStatus3xx enables caching of redirect responses (default: false).
	CacheStatus3xx bool `json:",optional" yaml:",optional"`

	// CacheStatus4xx enables caching of client-error responses (default: false).
	CacheStatus4xx bool `json:",optional" yaml:",optional"`

	// CacheStatus5xx enables caching of server-error responses (default: false).
	CacheStatus5xx bool `json:",optional" yaml:",optional"`
}

// ToServiceConfig converts the YAML configuration to a runtime Config.
//
// repo is the storage backend (required).
// logger, metrics, and tracer are optional observability hooks — pass nil to
// use the no-op defaults.
func (f ConfigFile) ToServiceConfig(
	repo repository.IdempotencyRecordRepository,
	logger port.Logger,
	metrics port.Metrics,
	tracer port.Tracer,
) (Config, error) {
	if repo == nil {
		return Config{}, fmt.Errorf("idempotency: repository is required")
	}

	dupPolicy := domainservice.DuplicatePolicy(f.DuplicatePolicy)
	if dupPolicy == "" {
		dupPolicy = domainservice.DuplicateReject
	}

	cfg := Config{
		Disabled:   f.Disabled,
		Scope:      f.Scope,
		Repository: repo,
		Logger:     logger,
		Metrics:    metrics,
		Tracer:     tracer,
		Policy: domainservice.NewIdempotencyPolicy(dupPolicy, domainservice.TTLPolicy{
			ProcessingTTL: durationOrDefault(f.ProcessingTTL, 30*time.Second),
			CompletedTTL:  durationOrDefault(f.CompletedTTL, 24*time.Hour),
			FailedTTL:     durationOrDefault(f.FailedTTL, 5*time.Minute),
		}),
		WaitTimeout:  durationOrDefault(f.WaitTimeout, 5*time.Second),
		WaitInterval: durationOrDefault(f.WaitInterval, 50*time.Millisecond),
		CaptureRules: domainservice.NewCaptureRules(domainservice.CaptureRules{
			CacheStatus2xx:  boolPtrOrDefault(f.Capture.CacheStatus2xx, true),
			CacheStatus3xx:  f.Capture.CacheStatus3xx,
			CacheStatus4xx:  f.Capture.CacheStatus4xx,
			CacheStatus5xx:  f.Capture.CacheStatus5xx,
			MaxBodyBytes:    int64OrDefault(f.Capture.MaxBodyBytes, 1<<20),
			ContentTypes:    sliceOrDefault(f.Capture.ContentTypes, []string{"application/json"}),
			ExcludedHeaders: sliceOrDefault(f.Capture.ExcludedHeaders, []string{"Set-Cookie", "Authorization", "Cookie", "WWW-Authenticate"}),
		}),
	}

	// Fingerprinter
	fp := NewSHA256Fingerprinter()
	if f.Fingerprint.IncludeTenant != nil {
		fp.IncludeTenant = *f.Fingerprint.IncludeTenant
	}
	if f.Fingerprint.IncludeUser != nil {
		fp.IncludeUser = *f.Fingerprint.IncludeUser
	}
	if f.Fingerprint.IncludeBody != nil {
		fp.IncludeBody = *f.Fingerprint.IncludeBody
	}
	if f.Fingerprint.MaxBodyBytes > 0 {
		fp.MaxBodyBytes = f.Fingerprint.MaxBodyBytes
	}
	cfg.Fingerprinter = fp
	// Key resolver
	headerName := f.Key.HeaderName
	if headerName == "" {
		headerName = "Idempotency-Key"
	}
	cfg.KeyResolver = HeaderKeyResolver{
		HeaderName: headerName,
		Required:   boolPtrOrDefault(f.Key.Required, true),
	}

	return cfg, nil
}

// DefaultConfigFile returns a ConfigFile with safe production defaults.
func DefaultConfigFile() ConfigFile {
	return ConfigFile{
		Scope:              "",
		ProcessingTTL:      30 * time.Second,
		CompletedTTL:       24 * time.Hour,
		FailedTTL:          5 * time.Minute,
		DuplicatePolicy:    "reject",
		FailureMode:        "delete",
		StorageFailureMode: "fail_closed",
		WaitTimeout:        5 * time.Second,
		WaitInterval:       50 * time.Millisecond,
		Key: KeyConfig{
			HeaderName: "Idempotency-Key",
			Required:   boolPtr(true),
			MinLength:  16,
			MaxLength:  128,
		},
		Fingerprint: FingerprintConfig{
			IncludeTenant: boolPtr(true),
			IncludeUser:   boolPtr(true),
			IncludeBody:   boolPtr(true),
			MaxBodyBytes:  1 << 20,
		},
		Capture: CaptureConfig{
			MaxBodyBytes:    1 << 20,
			ContentTypes:    []string{"application/json"},
			ExcludedHeaders: []string{"Set-Cookie", "Authorization", "Cookie", "WWW-Authenticate"},
			CacheStatus2xx:  boolPtr(true),
		},
	}
}

// ---- helpers ----

func durationOrDefault(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

func int64OrDefault(v, def int64) int64 {
	if v <= 0 {
		return def
	}
	return v
}

// boolPtr returns a pointer to a bool. Useful for initialising *bool fields
// in DefaultConfigFile and test fixtures.
func boolPtr(v bool) *bool { return &v }

// boolPtrOrDefault resolves a *bool to a concrete bool, using def when the
// pointer is nil (i.e. the field was not explicitly set in YAML). This solves
// the Go zero-value problem for bool fields that default to true — an explicit
// false is now distinguishable from "not set".
func boolPtrOrDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func sliceOrDefault[T any](v []T, def []T) []T {
	if len(v) == 0 {
		return def
	}
	return v
}
