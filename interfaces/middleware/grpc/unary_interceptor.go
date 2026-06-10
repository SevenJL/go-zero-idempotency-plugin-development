// Package grpc provides a gRPC unary server interceptor for the idempotency
// plugin. It can be used directly with google.golang.org/grpc or wrapped for
// go-zero zrpc.
package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/command"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/dto"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/application/port"
	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/domain/valueobject"
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
//
// To enable heartbeat (TTL renewal for long-running handlers), use
// UnaryServerInterceptorWithHeartbeat instead.
func UnaryServerInterceptor(svc *appservice.IdempotencyService, registry port.RPCCodecRegistry) grpc.UnaryServerInterceptor {
	return UnaryServerInterceptorWithHeartbeat(svc, registry, nil)
}

// UnaryServerInterceptorWithHeartbeat returns a gRPC UnaryServerInterceptor
// with optional heartbeat support. When hbCfg is non-nil, the interceptor
// starts a heartbeat for long-running handlers to prevent the idempotency
// record from expiring before the handler completes.
//
// Usage:
//
//	hbCfg := appservice.HeartbeatConfig{Repo: repo, TTL: 30 * time.Second}
//	interceptor := grpcidem.UnaryServerInterceptorWithHeartbeat(idemSvc, registry, &hbCfg)
func UnaryServerInterceptorWithHeartbeat(svc *appservice.IdempotencyService, registry port.RPCCodecRegistry, hbCfg *appservice.HeartbeatConfig) grpc.UnaryServerInterceptor {
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
				Scope:     valueobject.NewScope("", tenant, user),
				Metadata:  metadataToMap(ctx),
				Body:      reqBody,
			},
		})
		if err != nil {
			return nil, status.Errorf(beginErrorCode(err), "idempotency begin: %v", err)
		}

		switch beginResult.Type {
		case dto.BeginResultSkipped, dto.BeginResultPassThrough:
			return handler(ctx, req)

		case dto.BeginResultAcquired:
			// Start heartbeat for long-running handlers.
			var hb *appservice.Heartbeat
			if hbCfg != nil {
				cfg := *hbCfg
				cfg.Key = beginResult.Key
				cfg.Scope = beginResult.Scope
				cfg.Owner = beginResult.Owner
				hb = appservice.NewHeartbeat(cfg)
				ctx = hb.Start(ctx)
			}

			resp, handlerErr := handler(ctx, req)

			// Stop heartbeat before finalising the record.
			if hb != nil {
				hb.Stop()
			}

			if handlerErr != nil {
				// Business handler failed — abort.
				if err := svc.Abort(ctx, command.AbortCommand{
					Key:          beginResult.Key,
					Fingerprint:  beginResult.Fingerprint,
					Owner:        beginResult.Owner,
					Scope:        beginResult.Scope,
					ErrorCode:    status.Code(handlerErr).String(),
					ErrorMessage: handlerErr.Error(),
				}); err != nil {
					log.Printf("idempotency: abort failed for method %s: %v", info.FullMethod, err)
				}
				return resp, handlerErr
			}

			// Business handler succeeded — cache the response.
			codec := lookupCodec(registry, info.FullMethod)
			respBody, _ := codec.Marshal(resp)

			if err := svc.Complete(ctx, command.CompleteCommand{
				Key:         beginResult.Key,
				Fingerprint: beginResult.Fingerprint,
				Owner:       beginResult.Owner,
				Scope:       beginResult.Scope,
				Response: dto.CapturedResponse{
					Codec: codec.ContentType(),
					Body:  respBody,
				},
			}); err != nil {
				// Best-effort: the handler already succeeded. Logging happens
				// inside the service layer.
			}
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

func beginErrorCode(err error) codes.Code {
	switch {
	case errors.Is(err, appservice.ErrMissingIdempotencyKey),
		errors.Is(err, valueobject.ErrEmptyIdempotencyKey),
		errors.Is(err, valueobject.ErrInvalidIdempotencyKey):
		return codes.InvalidArgument
	default:
		return codes.Internal
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
		// No codec registered for this method — replay cannot produce a
		// correctly typed response message. The stored body is available
		// but the gRPC framework requires a concrete proto message.
		// Register a codec and factory via RPCCodecRegistry.Register:
		//
		//   registry.Register("/package.Service/Method", protobufCodec,
		//       func() any { return &pb.Response{} })
		return nil, status.Error(codes.FailedPrecondition,
			"idempotency: no codec registered for replay; register a codec factory for this RPC method")
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
