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
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode"

	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/alts"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	flAddr          string
	flService       string
	flUserAgent     string
	flConnTimeout   time.Duration
	flRPCHeaders    = rpcHeaders{MD: make(metadata.MD)}
	flRPCTimeout    time.Duration
	flTLS           bool
	flTLSNoVerify   bool
	flTLSCACert     string
	flTLSClientCert string
	flTLSClientKey  string
	flTLSServerName string
	flALTS          bool
	flVerbose       bool
	flGZIP          bool
	flSPIFFE        bool
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
	// StatusSpiffeFailed indicates failure to retrieve credentials using spiffe workload API
	StatusSpiffeFailed = 20
)

func init() {
	flagSet := flag.NewFlagSet("", flag.ContinueOnError)
	log.SetFlags(0)
	flagSet.StringVar(&flAddr, "addr", "", "(required) tcp host:port to connect")
	flagSet.StringVar(&flService, "service", "", "service name to check (default: \"\")")
	flagSet.StringVar(&flUserAgent, "user-agent", "grpc_health_probe", "user-agent header value of health check requests")
	// timeouts
	flagSet.DurationVar(&flConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flagSet.Var(&flRPCHeaders, "rpc-header", "additional RPC headers in 'name: value' format. May specify more than one via multiple flags.")
	flagSet.DurationVar(&flRPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	// tls settings
	flagSet.BoolVar(&flTLS, "tls", false, "use TLS (default: false, INSECURE plaintext transport)")
	flagSet.BoolVar(&flTLSNoVerify, "tls-no-verify", false, "(with -tls) don't verify the certificate (INSECURE) presented by the server (default: false)")
	flagSet.StringVar(&flTLSCACert, "tls-ca-cert", "", "(with -tls, optional) file containing trusted certificates for verifying server")
	flagSet.StringVar(&flTLSClientCert, "tls-client-cert", "", "(with -tls, optional) client certificate for authenticating to the server (requires -tls-client-key)")
	flagSet.StringVar(&flTLSClientKey, "tls-client-key", "", "(with -tls) client private key for authenticating to the server (requires -tls-client-cert)")
	flagSet.StringVar(&flTLSServerName, "tls-server-name", "", "(with -tls) override the hostname used to verify the server certificate")
	flagSet.BoolVar(&flALTS, "alts", false, "use ALTS (default: false, INSECURE plaintext transport)")
	flagSet.BoolVar(&flVerbose, "v", false, "verbose logs")
	flagSet.BoolVar(&flGZIP, "gzip", false, "use GZIPCompressor for requests and GZIPDecompressor for response (default: false)")
	flagSet.BoolVar(&flSPIFFE, "spiffe", false, "use SPIFFE to obtain mTLS credentials")

	err := flagSet.Parse(os.Args[1:])
	if err != nil {
		os.Exit(StatusInvalidArguments)
	}

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
	if flALTS && flSPIFFE {
		argError("-alts and -spiffe are mutually incompatible")
	}
	if flTLS && flALTS {
		argError("cannot specify -tls with -alts")
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
		if flRPCHeaders.Len() > 0 {
			log.Printf("> headers: %s", flRPCHeaders)
		}
		log.Printf("> tls=%v", flTLS)
		if flTLS {
			log.Printf("  > no-verify=%v ", flTLSNoVerify)
			log.Printf("  > ca-cert=%s", flTLSCACert)
			log.Printf("  > client-cert=%s", flTLSClientCert)
			log.Printf("  > client-key=%s", flTLSClientKey)
			log.Printf("  > server-name=%s", flTLSServerName)
		}
		log.Printf("> alts=%v", flALTS)
		log.Printf("> spiffe=%v", flSPIFFE)
	}
}

type rpcHeaders struct{ metadata.MD }

func (s *rpcHeaders) String() string { return fmt.Sprintf("%v", s.MD) }

func (s *rpcHeaders) Set(value string) error {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid RPC header, expected 'key: value', got %q", value)
	}
	trimmed := strings.TrimLeftFunc(parts[1], unicode.IsSpace)
	s.Append(parts[0], trimmed)
	return nil
}

func buildCredentials(skipVerify bool, caCerts, clientCert, clientKey, serverName string) (credentials.TransportCredentials, error) {
	var cfg tls.Config

	if clientCert != "" && clientKey != "" {
		keyPair, err := tls.LoadX509KeyPair(clientCert, clientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load tls client cert/key pair. error=%v", err)
		}
		cfg.Certificates = []tls.Certificate{keyPair}
	}

	if skipVerify {
		cfg.InsecureSkipVerify = true
	} else if caCerts != "" {
		// override system roots
		rootCAs := x509.NewCertPool()
		pem, err := ioutil.ReadFile(caCerts)
		if err != nil {
			return nil, fmt.Errorf("failed to load root CA certificates from file (%s) error=%v", caCerts, err)
		}
		if !rootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no root CA certs parsed from file %s", caCerts)
		}
		cfg.RootCAs = rootCAs
	}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	return credentials.NewTLS(&cfg), nil
}

func main() {
	retcode := 0
	defer func() { os.Exit(retcode) }()

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

	opts := []grpc.DialOption{
		grpc.WithUserAgent(flUserAgent),
		grpc.WithBlock(),
	}
	if flTLS && flSPIFFE {
		log.Printf("-tls and -spiffe are mutually incompatible")
		retcode = StatusInvalidArguments
		return
	}
	if flTLS {
		creds, err := buildCredentials(flTLSNoVerify, flTLSCACert, flTLSClientCert, flTLSClientKey, flTLSServerName)
		if err != nil {
			log.Printf("failed to initialize tls credentials. error=%v", err)
			retcode = StatusInvalidArguments
			return
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else if flALTS {
		creds := alts.NewServerCreds(alts.DefaultServerOptions())
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else if flSPIFFE {
		spiffeCtx, _ := context.WithTimeout(ctx, flRPCTimeout)
		source, err := workloadapi.NewX509Source(spiffeCtx)
		if err != nil {
			log.Printf("failed to initialize tls credentials with spiffe. error=%v", err)
			retcode = StatusSpiffeFailed
			return
		}
		if flVerbose {
			svid, err := source.GetX509SVID()
			if err != nil {
				log.Fatalf("error getting x509 svid: %+v", err)
			}
			log.Printf("SPIFFE Verifiable Identity Document (SVID): %q", svid.ID)
		}
		creds := credentials.NewTLS(tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeAny()))
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	if flGZIP {
		opts = append(opts,
			grpc.WithCompressor(grpc.NewGZIPCompressor()),
			grpc.WithDecompressor(grpc.NewGZIPDecompressor()),
		)
	}

	if flVerbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	dialCtx, dialCancel := context.WithTimeout(ctx, flConnTimeout)
	defer dialCancel()
	conn, err := grpc.DialContext(dialCtx, flAddr, opts...)
	if err != nil {
		if err == context.DeadlineExceeded {
			log.Printf("timeout: failed to connect service %q within %v", flAddr, flConnTimeout)
		} else {
			log.Printf("error: failed to connect service at %q: %+v", flAddr, err)
		}
		retcode = StatusConnectionFailure
		return
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if flVerbose {
		log.Printf("connection established (took %v)", connDuration)
	}

	rpcStart := time.Now()
	rpcCtx, rpcCancel := context.WithTimeout(ctx, flRPCTimeout)
	defer rpcCancel()
	rpcCtx = metadata.NewOutgoingContext(rpcCtx, flRPCHeaders.MD)
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx,
		&healthpb.HealthCheckRequest{
			Service: flService})
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			log.Printf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health): %s", stat.Message())
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			log.Printf("timeout: health rpc did not complete within %v", flRPCTimeout)
		} else {
			log.Printf("error: health rpc failed: %+v", err)
		}
		retcode = StatusRPCFailure
		return
	}
	rpcDuration := time.Since(rpcStart)

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		log.Printf("service unhealthy (responded with %q)", resp.GetStatus().String())
		retcode = StatusUnhealthy
		return
	}
	if flVerbose {
		log.Printf("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}
	log.Printf("status: %v", resp.GetStatus().String())
}
