package redis

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/model"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
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
// storage. When encryptor is non-nil, the response body is encrypted via
// AES-GCM before encoding.
func marshalRecord(r *model.IdempotencyRecord, encryptor BodyEncryptor) ([]byte, error) {
	rr := toRedisRecord(r, encryptor)
	return json.Marshal(rr)
}

// unmarshalRecord reconstructs a domain IdempotencyRecord from JSON bytes read
// from Redis. When encryptor is non-nil, the stored body is decrypted after
// decoding.
func unmarshalRecord(data []byte, encryptor BodyEncryptor) (*model.IdempotencyRecord, error) {
	var rr redisRecord
	if err := json.Unmarshal(data, &rr); err != nil {
		return nil, err
	}
	return fromRedisRecord(rr, encryptor), nil
}

// encodeBody applies optional encryption to the response body.
func encodeBody(body []byte, encryptor BodyEncryptor) string {
	if encryptor != nil {
		encrypted, err := encryptor.Encrypt(body)
		if err == nil {
			return encrypted
		}
	}
	return base64.StdEncoding.EncodeToString(body)
}

// decodeBody reverses encodeBody.
func decodeBody(encoded string, encryptor BodyEncryptor) ([]byte, error) {
	if encryptor != nil {
		return encryptor.Decrypt(encoded)
	}
	return base64.StdEncoding.DecodeString(encoded)
}

// bodyEncoding returns the encoding label for the stored body.
func bodyEncoding(encryptor BodyEncryptor) string {
	if encryptor != nil {
		return "aes-gcm+base64"
	}
	return "base64"
}

func toRedisRecord(r *model.IdempotencyRecord, encryptor BodyEncryptor) redisRecord {
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
			Body:         encodeBody(resp.Body, encryptor),
			BodyEncoding: bodyEncoding(encryptor),
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

func fromRedisRecord(rr redisRecord, encryptor BodyEncryptor) *model.IdempotencyRecord {
	var resp model.CapturedResponse
	if rr.Response != nil {
		body, err := decodeBody(rr.Response.Body, encryptor)
		if err != nil {
			// If decryption fails (e.g. key rotation, corrupted data),
			// return an empty body rather than crashing.
			body = nil
		}
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
