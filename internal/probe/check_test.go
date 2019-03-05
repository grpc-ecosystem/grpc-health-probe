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
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestCheck_OK(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			return healthpb.HealthCheckResponse_SERVING, nil
		},
	}

	if err := Check(context.Background(), c, time.Second, ""); err != nil {
		t.Fatal(err)
	}
}

func TestCheck_serviceNamePassed(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(s string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			if s != "myService" {
				return 0, status.Error(codes.NotFound, fmt.Sprintf("got wrong service name=%q", s))
			}
			return healthpb.HealthCheckResponse_SERVING, nil
		},
	}

	if err := Check(context.Background(), c, time.Second, "foo"); err == nil {
		t.Fatal("expected error; got <nil>")
	}
	if err := Check(context.Background(), c, time.Second, "myService"); err != nil {
		t.Fatalf("was not expecting error: %+v", err)
	}
}

func TestCheck_genericError(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			return 0, errors.New("expected error")
		},
	}

	err := Check(context.Background(), c, time.Second, "")
	if err == nil {
		t.Fatal("was expecting error")
	}
	if _, ok := err.(ServingStatusError); ok {
		t.Fatalf("error should not be of ServingStatusError type: %+v", err)
	}
	expectedMsg := "error: health rpc failed: expected error"
	if errMsg := err.Error(); errMsg != expectedMsg {
		t.Fatalf("wrong error: expected=%q got=%q", expectedMsg, errMsg)
	}
}

func TestCheck_timeoutError(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			return 0, status.Error(codes.DeadlineExceeded, "some timeout error")
		},
	}

	err := Check(context.Background(), c, time.Second, "")
	if err == nil {
		t.Fatal("was expecting error")
	}
	if _, ok := err.(ServingStatusError); ok {
		t.Fatalf("error should not be of ServingStatusError type: %+v", err)
	}
	expectedMsg := "timeout: health rpc did not complete within 1s"
	if errMsg := err.Error(); errMsg != expectedMsg {
		t.Fatalf("wrong error: expected=%q got=%q", expectedMsg, errMsg)
	}
}

func TestCheck_unimplementedError(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			return 0, status.Error(codes.Unimplemented, "some unimplemented error")
		},
	}

	err := Check(context.Background(), c, time.Second, "")
	if err == nil {
		t.Fatal("was expecting error")
	}
	if _, ok := err.(ServingStatusError); ok {
		t.Fatalf("error should not be of ServingStatusError type: %+v", err)
	}
	expectedMsg := "error: this server does not implement the grpc health protocol (grpc.health.v1.Health)"
	if errMsg := err.Error(); errMsg != expectedMsg {
		t.Fatalf("wrong error: expected=%q got=%q", expectedMsg, errMsg)
	}
}

func TestCheck_servingStatusError(t *testing.T) {
	c := &mockHealthClient{
		checkFunc: func(string) (healthpb.HealthCheckResponse_ServingStatus, error) {
			return healthpb.HealthCheckResponse_NOT_SERVING, nil
		},
	}

	err := Check(context.Background(), c, time.Second, "")
	if err == nil {
		t.Fatal("was expecting error")
	}
	if _, ok := err.(ServingStatusError); !ok {
		t.Fatalf("error is not of ServingStatusError type: %+v", err)
	}
	expectedMsg := `service unhealthy (responded with "NOT_SERVING")`
	if errMsg := err.Error(); errMsg != expectedMsg {
		t.Fatalf("error message wrong: expected=%q got=%q", expectedMsg, errMsg)
	}
}
