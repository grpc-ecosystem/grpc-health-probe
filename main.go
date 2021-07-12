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
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Flags struct {
	Addr          string
	Service       string
	UserAgent     string
	ConnTimeout   time.Duration
	RPCHeaders    rpcHeaders
	RPCTimeout    time.Duration
	TLS           bool
	TLSNoVerify   bool
	TLSCACert     string
	TLSClientCert string
	TLSClientKey  string
	TLSServerName string
	Verbose       bool
	GZIP          bool
	SPIFFE        bool
}

var fl Flags

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

func parseFlags(args []string) error {
	flagSet := flag.NewFlagSet("", flag.ContinueOnError)
	fl = Flags{}
	log.SetFlags(0)
	flagSet.StringVar(&fl.Addr, "addr", "", "(required) tcp host:port to connect")
	flagSet.StringVar(&fl.Service, "service", "", "service name to check (default: \"\")")
	flagSet.StringVar(&fl.UserAgent, "user-agent", "grpc_health_probe", "user-agent header value of health check requests")
	flagSet.Var(&fl.RPCHeaders, "rpc-header", "additional RPC headers in 'name: value' format. May specify more than one via multiple flags.")
	// timeouts
	flagSet.DurationVar(&fl.ConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flagSet.DurationVar(&fl.RPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	// tls settings
	flagSet.BoolVar(&fl.TLS, "tls", false, "use TLS (default: false, INSECURE plaintext transport)")
	flagSet.BoolVar(&fl.TLSNoVerify, "tls-no-verify", false, "(with -tls) don't verify the certificate (INSECURE) presented by the server (default: false)")
	flagSet.StringVar(&fl.TLSCACert, "tls-ca-cert", "", "(with -tls, optional) file containing trusted certificates for verifying server")
	flagSet.StringVar(&fl.TLSClientCert, "tls-client-cert", "", "(with -tls, optional) client certificate for authenticating to the server (requires -tls-client-key)")
	flagSet.StringVar(&fl.TLSClientKey, "tls-client-key", "", "(with -tls) client private key for authenticating to the server (requires -tls-client-cert)")
	flagSet.StringVar(&fl.TLSServerName, "tls-server-name", "", "(with -tls) override the hostname used to verify the server certificate")
	flagSet.BoolVar(&fl.Verbose, "v", false, "verbose logs")
	flagSet.BoolVar(&fl.GZIP, "gzip", false, "use GZIPCompressor for requests and GZIPDecompressor for response (default: false)")
	flagSet.BoolVar(&fl.SPIFFE, "spiffe", false, "use SPIFFE to obtain mTLS credentials")

	err := flagSet.Parse(args)
	if err != nil {
		return err
	}

	if fl.Addr == "" {
		return fmt.Errorf("-addr not specified")
	}
	if fl.ConnTimeout <= 0 {
		return fmt.Errorf("-connect-timeout must be greater than zero (specified: %v)", fl.ConnTimeout)
	}
	if fl.RPCTimeout <= 0 {
		return fmt.Errorf("-rpc-timeout must be greater than zero (specified: %v)", fl.RPCTimeout)
	}
	if !fl.TLS && fl.TLSNoVerify {
		return fmt.Errorf("specified -tls-no-verify without specifying -tls")
	}
	if !fl.TLS && fl.TLSCACert != "" {
		return fmt.Errorf("specified -tls-ca-cert without specifying -tls")
	}
	if !fl.TLS && fl.TLSClientCert != "" {
		return fmt.Errorf("specified -tls-client-cert without specifying -tls")
	}
	if !fl.TLS && fl.TLSServerName != "" {
		return fmt.Errorf("specified -tls-server-name without specifying -tls")
	}
	if fl.TLSClientCert != "" && fl.TLSClientKey == "" {
		return fmt.Errorf("specified -tls-client-cert without specifying -tls-client-key")
	}
	if fl.TLSClientCert == "" && fl.TLSClientKey != "" {
		return fmt.Errorf("specified -tls-client-key without specifying -tls-client-cert")
	}
	if fl.TLSNoVerify && fl.TLSCACert != "" {
		return fmt.Errorf("cannot specify -tls-ca-cert with -tls-no-verify (CA cert would not be used)")
	}
	if fl.TLSNoVerify && fl.TLSServerName != "" {
		return fmt.Errorf("cannot specify -tls-server-name with -tls-no-verify (server name would not be used)")
	}

	if fl.Verbose {
		log.Printf("parsed options:")
		log.Printf("> addr=%s conn_timeout=%v rpc_timeout=%v", fl.Addr, fl.ConnTimeout, fl.RPCTimeout)
		if fl.RPCHeaders.Len() > 0 {
			log.Printf("> headers: %s", fl.RPCHeaders)
		}
		log.Printf("> tls=%v", fl.TLS)
		if fl.TLS {
			log.Printf("  > no-verify=%v ", fl.TLSNoVerify)
			log.Printf("  > ca-cert=%s", fl.TLSCACert)
			log.Printf("  > client-cert=%s", fl.TLSClientCert)
			log.Printf("  > client-key=%s", fl.TLSClientKey)
			log.Printf("  > server-name=%s", fl.TLSServerName)
		}
		log.Printf("> spiffe=%v", fl.SPIFFE)
	}
	return nil
}

type rpcHeaders struct{ metadata.MD }

func (s *rpcHeaders) String() string { return fmt.Sprintf("%v", s.MD) }

func (s *rpcHeaders) Set(value string) error {
	if s.MD == nil {
		s.MD = make(metadata.MD)
	}
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

func probe(args ...string) int {
	if err := parseFlags(args); err != nil {
		log.Printf("error: %v", err)
		return StatusInvalidArguments
	}
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
		grpc.WithUserAgent(fl.UserAgent),
		grpc.WithBlock(),
	}
	if fl.TLS && fl.SPIFFE {
		log.Printf("-tls and -spiffe are mutually incompatible")
		return StatusInvalidArguments
	}
	if fl.TLS {
		creds, err := buildCredentials(fl.TLSNoVerify, fl.TLSCACert, fl.TLSClientCert, fl.TLSClientKey, fl.TLSServerName)
		if err != nil {
			log.Printf("failed to initialize tls credentials. error=%v", err)
			return StatusInvalidArguments
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else if fl.SPIFFE {
		spiffeCtx, _ := context.WithTimeout(ctx, fl.RPCTimeout)
		source, err := workloadapi.NewX509Source(spiffeCtx)
		if err != nil {
			log.Printf("failed to initialize tls credentials with spiffe. error=%v", err)
			return StatusSpiffeFailed
		}
		if fl.Verbose {
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

	if fl.GZIP {
		opts = append(opts,
			grpc.WithCompressor(grpc.NewGZIPCompressor()),
			grpc.WithDecompressor(grpc.NewGZIPDecompressor()),
		)
	}

	if fl.Verbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	dialCtx, dialCancel := context.WithTimeout(ctx, fl.ConnTimeout)
	defer dialCancel()
	conn, err := grpc.DialContext(dialCtx, fl.Addr, opts...)
	if err != nil {
		if err == context.DeadlineExceeded {
			log.Printf("timeout: failed to connect service %q within %v", fl.Addr, fl.ConnTimeout)
		} else {
			log.Printf("error: failed to connect service at %q: %+v", fl.Addr, err)
		}
		return StatusConnectionFailure
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if fl.Verbose {
		log.Printf("connection established (took %v)", connDuration)
	}

	rpcStart := time.Now()
	rpcCtx, rpcCancel := context.WithTimeout(ctx, fl.RPCTimeout)
	defer rpcCancel()
	rpcCtx = metadata.NewOutgoingContext(ctx, fl.RPCHeaders.MD)
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx,
		&healthpb.HealthCheckRequest{
			Service: fl.Service})
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			log.Printf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health): %s", stat.Message())
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			log.Printf("timeout: health rpc did not complete within %v", fl.RPCTimeout)
		} else {
			log.Printf("error: health rpc failed: %+v", err)
		}
		return StatusRPCFailure
	}
	rpcDuration := time.Since(rpcStart)

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		log.Printf("service unhealthy (responded with %q)", resp.GetStatus().String())
		return StatusUnhealthy
	}
	if fl.Verbose {
		log.Printf("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}
	log.Printf("status: %v", resp.GetStatus().String())
	return 0
}

func main() {
	os.Exit(probe(os.Args[1:]...))
}
