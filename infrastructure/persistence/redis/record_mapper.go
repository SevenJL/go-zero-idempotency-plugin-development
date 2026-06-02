package redis

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/your-org/go-idempotency/domain/model"
	"github.com/your-org/go-idempotency/domain/valueobject"
)

// redisRecord is the JSON wire format stored in Redis. It mirrors the schema
// described in the development documentation §7.2.
// All fields are exported for encoding/json.
type redisRecord struct {
	Version     int    `json:"version"`
	Status      string `json:"status"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Owner       string `json:"owner"`
	Operation   string `json:"operation,omitempty"`
	Service     string `json:"service,omitempty"`
	Tenant      string `json:"tenant,omitempty"`
	User        string `json:"user,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	ExpiresAt   int64  `json:"expires_at"`

	Response *redisCapturedResponse `json:"response,omitempty"`
	Error    *redisError            `json:"error,omitempty"`
}

type redisCapturedResponse struct {
	StatusCode   int                 `json:"status_code"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         string              `json:"body,omitempty"`
	BodyEncoding string              `json:"body_encoding,omitempty"`
	Codec        string              `json:"codec,omitempty"`
}

type redisError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// marshalRecord converts a domain IdempotencyRecord to JSON bytes for Redis
// storage. It uses only the public accessor methods, respecting DDD encapsulation.
func marshalRecord(r *model.IdempotencyRecord) ([]byte, error) {
	rr := toRedisRecord(r)
	return json.Marshal(rr)
}

// unmarshalRecord reconstructs a domain IdempotencyRecord from JSON bytes read
// from Redis. It uses model.RestoreRecord to bypass construction-time validation
// (the stored data is already trusted).
func unmarshalRecord(data []byte) (*model.IdempotencyRecord, error) {
	var rr redisRecord
	if err := json.Unmarshal(data, &rr); err != nil {
		return nil, err
	}
	return fromRedisRecord(rr), nil
}

func toRedisRecord(r *model.IdempotencyRecord) redisRecord {
	rr := redisRecord{
		Version:     1,
		Status:      r.Status().String(),
		Key:         r.Key().String(),
		Fingerprint: r.Fingerprint().String(),
		Owner:       r.Owner().String(),
		Operation:   r.Operation().String(),
		Service:     r.Scope().Service,
		Tenant:      r.Scope().Tenant,
		User:        r.Scope().User,
		CreatedAt:   r.CreatedAt().UnixMilli(),
		UpdatedAt:   r.UpdatedAt().UnixMilli(),
		ExpiresAt:   r.ExpiresAt().UnixMilli(),
	}

	resp := r.Response()
	// Only store if the response is meaningful.
	if !resp.IsEmpty() {
		rr.Response = &redisCapturedResponse{
			StatusCode:   resp.StatusCode,
			Headers:      resp.Headers,
			Body:         base64.StdEncoding.EncodeToString(resp.Body),
			BodyEncoding: "base64",
			Codec:        resp.Codec,
		}
	}

	if ec := r.ErrorCode(); ec != "" {
		rr.Error = &redisError{
			Code:    ec,
			Message: r.ErrorMessage(),
		}
	}

	return rr
}

func fromRedisRecord(rr redisRecord) *model.IdempotencyRecord {
	var resp model.CapturedResponse
	if rr.Response != nil {
		body, _ := base64.StdEncoding.DecodeString(rr.Response.Body)
		resp = model.CapturedResponse{
			StatusCode: rr.Response.StatusCode,
			Headers:    rr.Response.Headers,
			Body:       body,
			Codec:      rr.Response.Codec,
		}
	}

	var errCode, errMsg string
	if rr.Error != nil {
		errCode = rr.Error.Code
		errMsg = rr.Error.Message
	}

	// Use Unsafe* constructors because the data comes from trusted storage.
	return model.RestoreRecord(model.RestoreRecordParams{
		Key:          valueobject.UnsafeIdempotencyKey(rr.Key),
		Fingerprint:  valueobject.UnsafeFingerprint(rr.Fingerprint),
		Owner:        valueobject.UnsafeOwner(rr.Owner),
		Operation:    valueobject.UnsafeOperation(rr.Operation),
		Scope:        valueobject.Scope{Service: rr.Service, Tenant: rr.Tenant, User: rr.User},
		Status:       model.IdempotencyStatus(rr.Status),
		Response:     resp,
		ErrorCode:    errCode,
		ErrorMessage: errMsg,
		CreatedAt:    time.UnixMilli(rr.CreatedAt),
		UpdatedAt:    time.UnixMilli(rr.UpdatedAt),
		ExpiresAt:    time.UnixMilli(rr.ExpiresAt),
	})
}
