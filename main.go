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

	"github.com/grpc-ecosystem/grpc-health-probe/internal/probe"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

var (
	flAddr          string
	flService       string
	flConnTimeout   time.Duration
	flRPCTimeout    time.Duration
	flTLS           bool
	flTLSNoVerify   bool
	flTLSCACert     string
	flTLSClientCert string
	flTLSClientKey  string
	flTLSServerName string
	flVerbose       bool
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
	flag.StringVar(&flAddr, "addr", "", "(required) tcp host:port to connect")
	flag.StringVar(&flService, "service", "", "service name to check (default: \"\")")
	// timeouts
	flag.DurationVar(&flConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flag.DurationVar(&flRPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	// tls settings
	flag.BoolVar(&flTLS, "tls", false, "use TLS (default: false, INSECURE plaintext transport)")
	flag.BoolVar(&flTLSNoVerify, "tls-no-verify", false, "(with -tls) don't verify the certificate (INSECURE) presented by the server (default: false)")
	flag.StringVar(&flTLSCACert, "tls-ca-cert", "", "(with -tls, optional) file containing trusted certificates for verifying server")
	flag.StringVar(&flTLSClientCert, "tls-client-cert", "", "(with -tls, optional) client certificate for authenticating to the server (requires -tls-client-key)")
	flag.StringVar(&flTLSClientKey, "tls-client-key", "", "(with -tls) client private key for authenticating to the server (requires -tls-client-cert)")
	flag.StringVar(&flTLSServerName, "tls-server-name", "", "(with -tls) override the hostname used to verify the server certificate")
	flag.BoolVar(&flVerbose, "v", false, "verbose logs")

	flag.Parse()

	argError := func(s string, v ...interface{}) {
		log.Printf("error: "+s, v...)
		os.Exit(StatusInvalidArguments)
	}

	if flAddr == "" {
		argError("-addr not specified")
	}
	if flConnTimeout <= 0 {
		argError("-connect-timeout must be greater than zero (specified: %v)", flConnTimeout)
	}
	if flRPCTimeout <= 0 {
		argError("-rpc-timeout must be greater than zero (specified: %v)", flRPCTimeout)
	}
	if !flTLS && flTLSNoVerify {
		argError("specified -tls-no-verify without specifying -tls")
	}
	if !flTLS && flTLSCACert != "" {
		argError("specified -tls-ca-cert without specifying -tls")
	}
	if !flTLS && flTLSClientCert != "" {
		argError("specified -tls-client-cert without specifying -tls")
	}
	if !flTLS && flTLSServerName != "" {
		argError("specified -tls-server-name without specifying -tls")
	}
	if flTLSClientCert != "" && flTLSClientKey == "" {
		argError("specified -tls-client-cert without specifying -tls-client-key")
	}
	if flTLSClientCert == "" && flTLSClientKey != "" {
		argError("specified -tls-client-key without specifying -tls-client-cert")
	}
	if flTLSNoVerify && flTLSCACert != "" {
		argError("cannot specify -tls-ca-cert with -tls-no-verify (CA cert would not be used)")
	}
	if flTLSNoVerify && flTLSServerName != "" {
		argError("cannot specify -tls-server-name with -tls-no-verify (server name would not be used)")
	}

	if flVerbose {
		log.Printf("parsed options:")
		log.Printf("> addr=%s conn_timeout=%v rpc_timeout=%v", flAddr, flConnTimeout, flRPCTimeout)
		log.Printf("> tls=%v", flTLS)
		if flTLS {
			log.Printf("  > no-verify=%v ", flTLSNoVerify)
			log.Printf("  > ca-cert=%s", flTLSCACert)
			log.Printf("  > client-cert=%s", flTLSClientCert)
			log.Printf("  > client-key=%s", flTLSClientKey)
			log.Printf("  > server-name=%s", flTLSServerName)
		}
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

	var creds credentials.TransportCredentials
	if flTLS {
		c, err := probe.BuildCredentials(flTLSNoVerify, flTLSCACert, flTLSClientCert, flTLSClientKey, flTLSServerName)
		if err != nil {
			log.Printf("failed to initialize tls credentials. error=%v", err)
			os.Exit(StatusInvalidArguments)
		}
		creds = c
	}

	if flVerbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	conn, err := probe.Connect(ctx, flAddr, creds, flConnTimeout)
	if err != nil {
		log.Print(err)
		os.Exit(StatusConnectionFailure)
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if flVerbose {
		log.Printf("connection establisted (took %v)", connDuration)
	}

	rpcStart := time.Now()
	err = Check(rpcCtx, healthpb.NewHealthClient(conn), flRPCTimeout, flService)
	rpcDuration := time.Since(rpcStart)
	if err != nil {
		if _, ok := err.(ServingStatusError); ok {
			log.Print(err)
			os.Exit(StatusUnhealthy)
		}
		log.Print(err)
		os.Exit(StatusRPCFailure)
	}
	if flVerbose {
		log.Printf("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}
	log.Printf("status: %v", resp.GetStatus().String())
}