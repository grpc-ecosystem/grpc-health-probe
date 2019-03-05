// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package probe

import (
	"context"
	"errors"
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

func makeServer(t *testing.T, serverOptions ...grpc.ServerOption) (string, func()) {
	lis, err := net.Listen("tcp", "localhost:0") // assign port by the system
	if err != nil {
		t.Fatalf("failed to listen for mock server: %+v", err)
	}

	srv := grpc.NewServer(serverOptions...)
	healthpb.RegisterHealthServer(srv, newMockHealthService())
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

type mockHealth struct{}

func (m *mockHealth) Check(ctx context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (m *mockHealth) Watch(*healthpb.HealthCheckRequest, healthpb.Health_WatchServer) error {
	return errors.New("not implemented")
}

func newMockHealthService() healthpb.HealthServer {
	return &mockHealth{}
}
