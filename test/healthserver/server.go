package healthserver

import (
	"context"
	healthpb "github.com/grpc-ecosystem/grpc-health-probe/test/healthserver/proto"
	"google.golang.org/grpc"
	"log"
	"net"
)

type server struct {
}

var (
	grpcServer *grpc.Server
	status healthpb.HealthCheckResponse_ServingStatus
)
const GRPC_TEST_ADDRESS = "0.0.0.0:15000"

func (*server) Check(ctx context.Context, request *healthpb.HealthCheckRequest) (response *healthpb.HealthCheckResponse, err error) {
	return &healthpb.HealthCheckResponse{Status: status}, nil
}

func (*server) Watch(request *healthpb.HealthCheckRequest, hs healthpb.Health_WatchServer) error {
	return hs.Send(&healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING})
}

func Start() {
	lis, err := net.Listen("tcp", GRPC_TEST_ADDRESS)
	if err != nil {
		log.Fatalf("Error %v", err)
	}
	status = healthpb.HealthCheckResponse_SERVING
	grpcServer = grpc.NewServer()
	healthpb.RegisterHealthServer(grpcServer, &server{})
	grpcServer.Serve(lis)
}
func Stop() {
	grpcServer.Stop()
}
func SetStatus(newStatus healthpb.HealthCheckResponse_ServingStatus) {
	status = newStatus
}