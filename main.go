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
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	flAddr        string
	flConnTimeout time.Duration
	flRPCTimeout  time.Duration
)

const (
	// StatusInvalidArguments indicates specified invalid arguments.
	StatusInvalidArguments = 1
	// StatusConnectionFailure indicates connection failed.
	StatusConnectionFailure = 2
	// StatusRPCFailure indicates rpc failed.
	StatusRPCFailure = 3
	// StatusUnhealthy indicates rpc succeeded but indicates unhealthy service.
	StatusUnhealthy = 4
)

func init() {
	log.SetFlags(0)

	// TODO(ahmetb) add flags for -insecure and authentication
	flag.DurationVar(&flConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flag.DurationVar(&flRPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	flag.StringVar(&flAddr, "addr", "", "(required) tcp host:port to connect")
	flag.Parse()

	if flAddr == "" {
		log.Println("-addr not specified")
		os.Exit(StatusInvalidArguments)
	}
	if flConnTimeout <= 0 {
		log.Printf("-connect-timeout must be greater than zero (specified: %v)", flConnTimeout)
		os.Exit(StatusInvalidArguments)
	}
	if flRPCTimeout <= 0 {
		log.Printf("-rpc-timeout must be greater than zero (specified: %v)", flRPCTimeout)
		os.Exit(StatusInvalidArguments)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		sig := <-c
		if sig == os.Interrupt {
			log.Printf("cancellation received")
			cancel()
			return
		}
	}()
	log.Printf("establishing connection")
	conn, err := grpc.DialContext(ctx, flAddr,
		grpc.WithInsecure(),
		grpc.WithUserAgent("grpc_health_probe"),
		grpc.WithBlock(),
		grpc.WithTimeout(flConnTimeout))
	if err != nil {
		log.Printf("failed to connect service at %q: %+v", flAddr, err)
		os.Exit(StatusConnectionFailure)
	}
	defer conn.Close()

	rpcCtx, rpcCancel := context.WithTimeout(ctx, flRPCTimeout)
	defer rpcCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		log.Printf("health check rpc failed: %+v", err)
		os.Exit(StatusRPCFailure)
	}

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		log.Printf("service unhealthy (responded with %q)", resp.GetStatus().String())
		os.Exit(StatusUnhealthy)
	}
	log.Printf("healthy: %v", resp.GetStatus().String())
}
