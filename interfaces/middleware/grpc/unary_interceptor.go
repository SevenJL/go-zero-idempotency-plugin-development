// Package grpc provides a gRPC unary server interceptor for the idempotency
// plugin. It can be used directly with google.golang.org/grpc or wrapped for
// go-zero zrpc.
package grpc

import (
	"context"
	"encoding/json"

	"github.com/your-org/go-idempotency/application/command"
	"github.com/your-org/go-idempotency/application/dto"
	"github.com/your-org/go-idempotency/application/port"
	appservice "github.com/your-org/go-idempotency/application/service"
	"github.com/your-org/go-idempotency/domain/valueobject"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns a gRPC UnaryServerInterceptor that provides
// idempotency protection for unary RPC methods.
//
// Usage:
//
//	s := grpc.NewServer(
//	    grpc.UnaryInterceptor(grpcidem.UnaryServerInterceptor(idemSvc, codecRegistry)),
//	)
func UnaryServerInterceptor(svc *appservice.IdempotencyService, registry port.RPCCodecRegistry) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip if no key is provided — idempotency is opt-in at the client.
		idemKey := extractFromMetadata(ctx, "idempotency-key")
		if idemKey == "" {
			return handler(ctx, req)
		}

		// Serialise the request body for fingerprint computation.
		reqBody, _ := json.Marshal(req)

		// Extract scope from gRPC metadata.
		tenant := extractFromMetadata(ctx, "tenant-id")
		user := extractFromMetadata(ctx, "user-id")

		beginResult, err := svc.Begin(ctx, command.BeginCommand{
			Request: dto.RequestContext{
				Operation: valueobject.UnsafeOperation(info.FullMethod),
				Scope:     valueobject.Scope{Tenant: tenant, User: user},
				Metadata:  metadataToMap(ctx),
				Body:      reqBody,
			},
		})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "idempotency begin: %v", err)
		}

		switch beginResult.Type {
		case dto.BeginResultSkipped:
			return handler(ctx, req)

		case dto.BeginResultAcquired:
			resp, handlerErr := handler(ctx, req)

			if handlerErr != nil {
				// Business handler failed — abort. For gRPC we use delete mode
				// so the client can retry with a fresh key. The application layer
				// handles the mode setting via its own defaults.
				_ = svc.Abort(ctx, command.AbortCommand{
					Key:          beginResult.Key,
					Fingerprint:  beginResult.Fingerprint,
					Owner:        beginResult.Owner,
					ErrorCode:    status.Code(handlerErr).String(),
					ErrorMessage: handlerErr.Error(),
				})
				return resp, handlerErr
			}

			// Business handler succeeded — cache the response.
			codec := lookupCodec(registry, info.FullMethod)
			respBody, _ := codec.Marshal(resp)

			_ = svc.Complete(ctx, command.CompleteCommand{
				Key:         beginResult.Key,
				Fingerprint: beginResult.Fingerprint,
				Owner:       beginResult.Owner,
				Response: dto.CapturedResponse{
					Codec: codec.ContentType(),
					Body:  respBody,
				},
			})
			return resp, nil

		case dto.BeginResultReplay:
			return replayRPCResponse(ctx, registry, info.FullMethod, beginResult)

		case dto.BeginResultConflict:
			return nil, status.Error(codes.Aborted, "idempotency: fingerprint conflict")

		case dto.BeginResultInProgress:
			return nil, status.Error(codes.Aborted, "idempotency: request in progress")

		case dto.BeginResultFailed:
			return replayRPCResponse(ctx, registry, info.FullMethod, beginResult)

		default:
			return nil, status.Error(codes.Internal, "idempotency: unexpected begin result")
		}
	}
}

func extractFromMetadata(ctx context.Context, key string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

func metadataToMap(ctx context.Context) map[string][]string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	return map[string][]string(md)
}

func lookupCodec(registry port.RPCCodecRegistry, fullMethod string) port.RPCCodec {
	if registry == nil {
		return defaultCodec{}
	}
	codec, _, _ := registry.Lookup(fullMethod)
	if codec == nil {
		return defaultCodec{}
	}
	return codec
}

func replayRPCResponse(ctx context.Context, registry port.RPCCodecRegistry, fullMethod string, result dto.BeginResult) (any, error) {
	codec, factory, registered := port.RPCCodec(nil), (func() any)(nil), false
	if registry != nil {
		codec, factory, registered = registry.Lookup(fullMethod)
	}
	if codec == nil {
		codec = defaultCodec{}
	}

	if !registered || factory == nil {
		// No factory registered — return the raw body.
		// The caller is expected to register types for replay to work correctly.
		var resp any
		if len(result.Response.Body) > 0 {
			_ = json.Unmarshal(result.Response.Body, &resp)
		}
		return resp, nil
	}

	resp := factory()
	if len(result.Response.Body) > 0 {
		if err := codec.Unmarshal(result.Response.Body, resp); err != nil {
			return nil, status.Errorf(codes.Internal, "idempotency: failed to unmarshal replay response: %v", err)
		}
	}
	return resp, nil
}

// defaultCodec uses JSON as the fallback serialisation format.
type defaultCodec struct{}

func (defaultCodec) ContentType() string                { return "application/json" }
func (defaultCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (defaultCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

var _ port.RPCCodec = defaultCodec{}
