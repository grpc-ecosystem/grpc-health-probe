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
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type ServingStatusError healthpb.HealthCheckResponse_ServingStatus

func (s ServingStatusError) Error() string {
	return fmt.Sprintf("service unhealthy (responded with %q)",
		healthpb.HealthCheckResponse_ServingStatus(s).String())
}

func Check(ctx context.Context, client healthpb.HealthClient, timeout time.Duration, serviceName string) error {
	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := client.Check(rpcCtx, &healthpb.HealthCheckRequest{Service: serviceName})
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			return fmt.Errorf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health)")
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			return fmt.Errorf("timeout: health rpc did not complete within %v", timeout)
		}
		return fmt.Errorf("error: health rpc failed: %+v", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return ServingStatusError(resp.GetStatus())
	}
	return nil
}
