package main

import (
	"context"
	"errors"
	"log"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// startServing runs the gRPC server in a goroutine and returns the addr to
// connect on.
func startServing(t *testing.T, srv *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go srv.Serve(lis)
	// srv.Stop() closes the listener.
	t.Cleanup(srv.Stop)
	return lis.Addr().String()
}

func TestHealthProbe(t *testing.T) {
	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, health.NewServer())
	addr := startServing(t, srv)

	retcode := probe("-addr=" + addr)
	if retcode != 0 {
		t.Errorf("probe -addr=%s returned %d, want 0", addr, retcode)
	}
}

func TestHealthProbeUnimplemented(t *testing.T) {
	srv := grpc.NewServer()
	addr := startServing(t, srv)

	retcode := probe("-addr=" + addr)
	if retcode != StatusRPCFailure {
		t.Errorf("probe -addr=%s returned %d, want 0", addr, retcode)
	}
}

// HeaderHealthServer only returns "SERVING" if the expected header is set on
// the request.
type HeaderHealthServer struct {
	healthgrpc.UnimplementedHealthServer
}

func (s *HeaderHealthServer) Check(ctx context.Context, in *healthgrpc.HealthCheckRequest) (*healthgrpc.HealthCheckResponse, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		log.Println("metadata.FromIncomingContext failed")
		return nil, errors.New("metadata.FromIncomingContext failed")
	}
	values := md["key"]
	if len(values) != 1 || values[0] != "value" {
		log.Printf("invalid metadata, want key:[value], got %v", md)
		return nil, errors.New("invalid metadata")
	}
	return &healthgrpc.HealthCheckResponse{
		Status: healthgrpc.HealthCheckResponse_SERVING,
	}, nil
}

func TestRPCHeader(t *testing.T) {
	srv := grpc.NewServer()
	healthgrpc.RegisterHealthServer(srv, &HeaderHealthServer{})
	addr := startServing(t, srv)

	retcode := probe("-addr="+addr, "-rpc-header=key: value")
	if retcode != 0 {
		t.Errorf("probe -addr=%s returned %d, want 0", addr, retcode)
	}
}
