package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
)

var ErrMissingIdempotencyKey = errors.New("idempotency key is missing")

type HeaderKeyResolver struct {
	HeaderName string
	Required   bool
	MinLength  int
	MaxLength  int
}

func (r HeaderKeyResolver) Resolve(_ context.Context, request dto.RequestContext) (valueobject.IdempotencyKey, error) {
	headerName := r.HeaderName
	if headerName == "" {
		headerName = "Idempotency-Key"
	}

	value := firstHeaderValue(request.Headers, headerName)
	if value == "" {
		value = firstHeaderValue(request.Metadata, strings.ToLower(headerName))
	}
	if value == "" {
		if r.Required {
			return valueobject.IdempotencyKey{}, ErrMissingIdempotencyKey
		}
		return valueobject.IdempotencyKey{}, nil
	}

	minLength := r.MinLength
	if minLength <= 0 {
		minLength = valueobject.DefaultMinKeyLength
	}
	maxLength := r.MaxLength
	if maxLength <= 0 {
		maxLength = valueobject.DefaultMaxKeyLength
	}
	return valueobject.NewIdempotencyKeyWithLimits(value, minLength, maxLength)
}

type SHA256Fingerprinter struct {
	IncludeTenant bool
	IncludeUser   bool
	IncludeBody   bool
	MaxBodyBytes  int64
}

// NewSHA256Fingerprinter creates a fingerprinter with defaults suitable
// for production use. All dimensions are included by default.
func NewSHA256Fingerprinter() SHA256Fingerprinter {
	return SHA256Fingerprinter{
		IncludeTenant: true,
		IncludeUser:   true,
		IncludeBody:   true,
		MaxBodyBytes:  1 << 20,
	}
}

func (f SHA256Fingerprinter) Fingerprint(_ context.Context, request dto.RequestContext) (valueobject.Fingerprint, error) {
	var parts []string
	if f.IncludeTenant {
		parts = append(parts, request.Scope.Service(), request.Scope.Tenant())
	} else {
		parts = append(parts, request.Scope.Service())
	}
	if f.IncludeUser {
		parts = append(parts, request.Scope.User())
	}
	parts = append(parts, request.Operation.String())
	if f.IncludeBody && len(request.Body) > 0 {
		body := request.Body
		maxBytes := f.MaxBodyBytes
		if maxBytes <= 0 {
			maxBytes = 1 << 20
		}
		if int64(len(body)) > maxBytes {
			body = body[:maxBytes]
		}
		parts = append(parts, string(canonicalBody(body)))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return valueobject.NewFingerprint("sha256:" + hex.EncodeToString(sum[:]))
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

func (SystemClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

type RandomOwnerFactory struct{}

func (RandomOwnerFactory) NewOwner(_ context.Context) (valueobject.Owner, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return valueobject.Owner{}, err
	}
	return valueobject.NewOwner(hex.EncodeToString(buf))
}

func firstHeaderValue(headers map[string][]string, name string) string {
	if len(headers) == 0 {
		return ""
	}

	lowerName := strings.ToLower(name)
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if strings.ToLower(key) != lowerName {
			continue
		}
		values := headers[key]
		if len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func canonicalBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}

	// Canonical JSON keeps semantically identical request bodies from producing
	// different fingerprints solely because object fields arrived in a new order.
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	canonical, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return canonical
}
