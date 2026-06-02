package service

// CaptureRules expresses which HTTP/RPC responses are cacheable for replay.
// It is a pure domain policy — it has no dependency on any framework.
//
// Zero-value fields follow the "unconfigured means safe default" convention:
// CacheStatus2xx defaults to true; all others default to false.
type CaptureRules struct {
	// CacheStatus2xx controls whether successful responses are cached.
	CacheStatus2xx bool

	// CacheStatus3xx controls whether redirect responses are cached.
	CacheStatus3xx bool

	// CacheStatus4xx controls whether client-error responses are cached.
	CacheStatus4xx bool

	// CacheStatus5xx controls whether server-error responses are cached.
	CacheStatus5xx bool

	// MaxBodyBytes is the maximum response body size that will be cached.
	// Bodies larger than this are not cached. Zero means no limit.
	MaxBodyBytes int64

	// ContentTypes is the whitelist of content-type prefixes that may be cached.
	// An empty list allows all types.
	ContentTypes []string

	// ExcludedHeaders lists response header names that must never be stored.
	// Header matching is case-insensitive.
	ExcludedHeaders []string

	// defaultsApplied is set by NewCaptureRules to distinguish a deliberately
	// zero-valued struct from one that simply hasn't been configured yet.
	defaultsApplied bool
}

const defaultMaxBodyBytes = 1 << 20 // 1 MB

// NewCaptureRules returns a CaptureRules with safe defaults applied to any
// zero-valued fields. This follows the same "zero value means use default"
// convention as the rest of the configuration.
func NewCaptureRules(rules CaptureRules) CaptureRules {
	if !rules.defaultsApplied {
		rules.CacheStatus2xx = true
		// CacheStatus3xx, CacheStatus4xx, CacheStatus5xx default to false.
	}
	if rules.MaxBodyBytes == 0 {
		rules.MaxBodyBytes = defaultMaxBodyBytes
	}
	if len(rules.ContentTypes) == 0 {
		rules.ContentTypes = []string{"application/json"}
	}
	if len(rules.ExcludedHeaders) == 0 {
		rules.ExcludedHeaders = []string{"Set-Cookie", "Authorization", "Cookie"}
	}
	rules.defaultsApplied = true
	return rules
}

// DefaultCaptureRules returns a CaptureRules with all defaults applied.
func DefaultCaptureRules() CaptureRules {
	return NewCaptureRules(CaptureRules{})
}

// ShouldCache returns true when the given status code, content-type, and body
// size satisfy the caching policy.
func (r CaptureRules) ShouldCache(statusCode int, contentType string, bodySize int64) bool {
	if !r.isStatusCodeCacheable(statusCode) {
		return false
	}
	if !r.isContentTypeAllowed(contentType) {
		return false
	}
	if !r.isBodySizeAcceptable(bodySize) {
		return false
	}
	return true
}

// FilterHeaders returns a copy of headers with excluded headers removed.
func (r CaptureRules) FilterHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 || len(r.ExcludedHeaders) == 0 {
		return headers
	}

	excluded := make(map[string]struct{}, len(r.ExcludedHeaders))
	for _, h := range r.ExcludedHeaders {
		excluded[toLower(h)] = struct{}{}
	}

	filtered := make(map[string][]string, len(headers))
	for name, values := range headers {
		if _, skip := excluded[toLower(name)]; !skip {
			filtered[name] = values
		}
	}
	return filtered
}

// IsBodySizeAcceptable reports whether bodySize is within the configured limit.
func (r CaptureRules) IsBodySizeAcceptable(bodySize int64) bool {
	return r.isBodySizeAcceptable(bodySize)
}

func (r CaptureRules) isStatusCodeCacheable(code int) bool {
	switch {
	case code >= 200 && code < 300:
		return r.CacheStatus2xx
	case code >= 300 && code < 400:
		return r.CacheStatus3xx
	case code >= 400 && code < 500:
		return r.CacheStatus4xx
	case code >= 500:
		return r.CacheStatus5xx
	default:
		return false
	}
}

func (r CaptureRules) isContentTypeAllowed(contentType string) bool {
	if len(r.ContentTypes) == 0 {
		return true // no whitelist means allow all
	}
	// An empty content-type means the caller didn't set one. We treat this as
	// allowable — the whitelist exists to block known binary/stream types, not
	// to reject headers that were simply omitted.
	if contentType == "" {
		return true
	}
	for _, allowed := range r.ContentTypes {
		if len(contentType) >= len(allowed) &&
			toLower(contentType[:len(allowed)]) == toLower(allowed) {
			return true
		}
	}
	return false
}

func (r CaptureRules) isBodySizeAcceptable(bodySize int64) bool {
	if r.MaxBodyBytes <= 0 {
		return true // no limit
	}
	return bodySize <= r.MaxBodyBytes
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
