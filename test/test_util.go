package test

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func testdata(path string) string {
	_, currentFile, _, _ := runtime.Caller(0)
	basepath := filepath.Dir(currentFile)
	return filepath.Join(basepath, "testdata", path)
}

const GRPC_ADDRESS  = "0.0.0.0:15000"

func makeServer(t *testing.T, status healthpb.HealthCheckResponse_ServingStatus, serverOptions ...grpc.ServerOption) (string, func()) {
	lis, err := net.Listen("tcp", GRPC_ADDRESS) // assign port by the system
	require.NoError(t, err)

	srv := grpc.NewServer(serverOptions...)
	healthpb.RegisterHealthServer(srv, newMockHealthService(status))
	go srv.Serve(lis)
	return lis.Addr().String(), func() {
		srv.Stop()
	}
}


func connect(t *testing.T, addr string, dialOpts ...grpc.DialOption) *grpc.ClientConn {
	ctx, done := context.WithTimeout(context.Background(), time.Second*1)
	defer done()
	conn, err := grpc.DialContext(ctx, addr, append([]grpc.DialOption{grpc.WithBlock()}, dialOpts...)...)
	if err != nil {
		t.Fatalf("failed to dial server (%s): %+v", addr, err)
	}
	return conn
}

func makeRequest(t *testing.T, conn *grpc.ClientConn) {
	_, err := healthpb.NewHealthClient(conn).Check(context.TODO(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("rpc failed: %+v", err)
	}
}

type mockHealthService struct{
	status healthpb.HealthCheckResponse_ServingStatus
}

func (m *mockHealthService) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: m.status}, nil
}

func (m *mockHealthService) Watch(*healthpb.HealthCheckRequest, healthpb.Health_WatchServer) error {
	return errors.New("not implemented")
}

func newMockHealthService(currentStatus healthpb.HealthCheckResponse_ServingStatus) healthpb.HealthServer {
	return &mockHealthService{status:currentStatus}
}

type mockHealthClient struct {
	checkFunc func(string) (healthpb.HealthCheckResponse_ServingStatus, error)
}

func (m *mockHealthClient) Check(ctx context.Context, in *healthpb.HealthCheckRequest, opts ...grpc.CallOption) (*healthpb.HealthCheckResponse, error) {
	s, err := m.checkFunc(in.GetService())
	return &healthpb.HealthCheckResponse{
		Status: s,
	}, err
}

func (m *mockHealthClient) Watch(ctx context.Context, in *healthpb.HealthCheckRequest, opts ...grpc.CallOption) (healthpb.Health_WatchClient, error) {
	return nil, errors.New("not implemented")
}
