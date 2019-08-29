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

	grpc_health_probe "github.com/grpc-ecosystem/grpc-health-probe/pkg"
)

var (
	globalConfig = &grpc_health_probe.Config{}
)

func init() {
	log.SetFlags(0)
	flag.StringVar(&globalConfig.Addr, "addr", "", "(required) tcp host:port to connect")
	flag.StringVar(&globalConfig.Service, "service", "", "service name to check (default: \"\")")
	flag.StringVar(&globalConfig.UserAgent, "user-agent", "grpc_health_probe", "user-agent header value of health check requests")
	// timeouts
	flag.DurationVar(&globalConfig.ConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flag.DurationVar(&globalConfig.RPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	// tls settings
	flag.BoolVar(&globalConfig.TLS, "tls", false, "use TLS (default: false, INSECURE plaintext transport)")
	flag.BoolVar(&globalConfig.TLSNoVerify, "tls-no-verify", false, "(with -tls) don't verify the certificate (INSECURE) presented by the server (default: false)")
	flag.StringVar(&globalConfig.TLSCACert, "tls-ca-cert", "", "(with -tls, optional) file containing trusted certificates for verifying server")
	flag.StringVar(&globalConfig.TLSClientCert, "tls-client-cert", "", "(with -tls, optional) client certificate for authenticating to the server (requires -tls-client-key)")
	flag.StringVar(&globalConfig.TLSClientKey, "tls-client-key", "", "(with -tls) client private key for authenticating to the server (requires -tls-client-cert)")
	flag.StringVar(&globalConfig.TLSServerName, "tls-server-name", "", "(with -tls) override the hostname used to verify the server certificate")
	flag.BoolVar(&globalConfig.Verbose, "v", false, "verbose logs")

	flag.Parse()

	argError := func(s string, v ...interface{}) {
		log.Printf("error: "+s, v...)
		os.Exit(grpc_health_probe.StatusInvalidArguments)
	}

	if err := globalConfig.Validate(); err != nil {
		argError(err.Error())
	}

	if globalConfig.Verbose {
		log.Printf("parsed options:")
		log.Printf("> addr=%s conn_timeout=%v rpc_timeout=%v", globalConfig.Addr, globalConfig.ConnTimeout, globalConfig.RPCTimeout)
		log.Printf("> tls=%v", globalConfig.TLS)
		if globalConfig.TLS {
			log.Printf("  > no-verify=%v ", globalConfig.TLSNoVerify)
			log.Printf("  > ca-cert=%s", globalConfig.TLSCACert)
			log.Printf("  > client-cert=%s", globalConfig.TLSClientCert)
			log.Printf("  > client-key=%s", globalConfig.TLSClientKey)
			log.Printf("  > server-name=%s", globalConfig.TLSServerName)
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

	resp, err := grpc_health_probe.Check(ctx, globalConfig)
	if err != nil {
		log.Printf(err.Error())
		os.Exit(err.ExitCode)
	}
	log.Printf("status: %v", resp.GetStatus().String())
}
