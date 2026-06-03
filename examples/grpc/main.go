// Example gRPC server demonstrating the idempotency unary interceptor.
//
// Start the server:
//
//	go run .
//
// This example does not require protobuf code generation — it uses a simple
// gRPC service handler to demonstrate the interceptor integration.
//
// In a real project, generate protobuf stubs with:
//
//	protoc --go_out=. --go-grpc_out=. order.proto
//
// Then register the gRPC codec:
//
//	registry := codec.NewCodecRegistry(nil)
//	registry.Register("/order.OrderService/Create", codec.JSONCodec{}, func() any {
//	    return &orderpb.CreateOrderResp{}
//	})
//
//	s := grpc.NewServer(
//	    grpc.UnaryInterceptor(
//	        grpcidem.UnaryServerInterceptor(idemSvc, registry),
//	    ),
//	)
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	appservice "github.com/sevenjl/go-zero-idempotency-plugin-development/application/service"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/codec"
	"github.com/sevenjl/go-zero-idempotency-plugin-development/infrastructure/persistence/memory"
	grpcidem "github.com/sevenjl/go-zero-idempotency-plugin-development/interfaces/middleware/grpc"
)

// gRPCServer implements a simple gRPC service for demonstration.
type gRPCServer struct{}

func main() {
	// ---- Build the idempotency service ----
	repo := memory.NewIdempotencyRecordRepository()
	idemSvc, err := appservice.NewIdempotencyService(appservice.Config{
		Repository: repo,
		Scope:      "grpc-example",
		KeyResolver: appservice.HeaderKeyResolver{
			HeaderName: "idempotency-key",
			Required:   false,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create idempotency service: %v", err)
	}

	// ---- Set up gRPC codec registry ----
	// Register codecs for all gRPC methods that need idempotency protection.
	registry := codec.NewCodecRegistry(nil)
	registry.Register("/example.OrderService/CreateOrder", codec.JSONCodec{}, func() any {
		return &map[string]any{}
	})
	registry.Register("/example.OrderService/CancelOrder", codec.JSONCodec{}, func() any {
		return &map[string]any{}
	})

	// ---- Create gRPC server with idempotency interceptor ----
	s := grpc.NewServer(
		grpc.UnaryInterceptor(
			grpcidem.UnaryServerInterceptor(idemSvc, registry),
		),
	)

	// Register the demo service
	RegisterOrderServiceServer(s, &gRPCServer{})

	// ---- Start listening ----
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	defer lis.Close()

	fmt.Println("gRPC server starting on localhost:50051")
	fmt.Println("Endpoints:")
	fmt.Println("  /example.OrderService/CreateOrder  — idempotency-protected")
	fmt.Println("  /example.OrderService/CancelOrder  — idempotency-protected")
	fmt.Println()
	fmt.Println("Test with grpcurl:")
	fmt.Println(`  grpcurl -plaintext \`)
	fmt.Println(`    -H 'idempotency-key: test-key-0000000001' \`)
	fmt.Println(`    -d '{"sku":"test","qty":1}' \`)
	fmt.Println(`    localhost:50051 example.OrderService/CreateOrder`)

	// ---- Graceful shutdown ----
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down gracefully...")
	s.GracefulStop()
	fmt.Println("Server stopped.")
}

// ---- gRPC service stubs (inline — no code generation needed for demo) ----

type OrderServer interface {
	CreateOrder(context.Context, map[string]any) (map[string]any, error)
	CancelOrder(context.Context, map[string]any) (map[string]any, error)
}

func RegisterOrderServiceServer(s *grpc.Server, svc OrderServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "example.OrderService",
		HandlerType: (*OrderServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "CreateOrder",
				Handler:    createOrderHandler(svc),
			},
			{
				MethodName: "CancelOrder",
				Handler:    cancelOrderHandler(svc),
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "order.proto",
	}, svc)
}

func createOrderHandler(svc OrderServer) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var req map[string]any
		if err := dec(&req); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return svc.(OrderServer).CreateOrder(ctx, req)
	}
}

func cancelOrderHandler(svc OrderServer) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var req map[string]any
		if err := dec(&req); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return svc.(OrderServer).CancelOrder(ctx, req)
	}
}

func (s *gRPCServer) CreateOrder(ctx context.Context, req map[string]any) (map[string]any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	fmt.Printf("[gRPC] CreateOrder called — metadata: %v, request: %v\n", md, req)

	return map[string]any{
		"order_id": fmt.Sprintf("grpc-order-%d", ctx.Value("now")),
		"status":   "created",
		"sku":      req["sku"],
	}, nil
}

func (s *gRPCServer) CancelOrder(ctx context.Context, req map[string]any) (map[string]any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	fmt.Printf("[gRPC] CancelOrder called — metadata: %v, request: %v\n", md, req)

	return map[string]any{
		"order_id": req["order_id"],
		"status":   "cancelled",
	}, nil
}
