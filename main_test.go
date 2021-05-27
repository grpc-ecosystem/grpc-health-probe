package main

import (
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
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
	grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
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
