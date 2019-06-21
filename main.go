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
	"net/http"
	"os"
	"os/signal"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"golang.org/x/net/http2"
)

type ProbeConfig struct {
	flAddr          string
	flHTTPListenAddr string
	flHTTPListenPath string
	flService       string
	flUserAgent     string
	flConnTimeout   time.Duration
	flRPCTimeout    time.Duration
	flTLS           bool
	flTLSNoVerify   bool
	flTLSCACert     string
	flTLSClientCert string
	flTLSClientKey  string
	flTLSServerName string
	flHTTPSTLSServerCert string
	flHTTPSTLSServerKey string
	flHTTPSTLSVerifyCA string
	flHTTPSTLSVerifyClient bool
	flVerbose       bool
}

var (
	cfg = &ProbeConfig{}
)

type GrpcProbeError struct {
	Code int
    Message string
}

func NewGrpcProbeError(code int, message string) *GrpcProbeError {
    return &GrpcProbeError{
		Code: code,
        Message: message,
    }
}
func (e *GrpcProbeError) Error() string {
    return e.Message
}

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
	flag.StringVar(&cfg.flAddr, "addr", "", "(required) tcp host:port to connect")
	flag.StringVar(&cfg.flService, "service", "", "service name to check (default: \"\")")
	flag.StringVar(&cfg.flUserAgent, "user-agent", "grpc_health_probe", "user-agent header value of health check requests")
	// settings for HTTPS lisenter
	flag.StringVar(&cfg.flHTTPListenAddr, "http-listen-addr", "", "http host:port to listen")
	flag.StringVar(&cfg.flHTTPListenPath, "http-listen-path", "/", "path to listen for healthcheck traffic (default '/')")
	flag.StringVar(&cfg.flHTTPSTLSServerCert, "https-listen-cert", "", "TLS Server certificate to for HTTP listner")
	flag.StringVar(&cfg.flHTTPSTLSServerKey, "https-listen-key", "", "TLS Server certificate key to for HTTP listner")
	flag.StringVar(&cfg.flHTTPSTLSVerifyCA, "https-listen-ca", "", "Use CA to verify client requests against CA")
	flag.BoolVar(&cfg.flHTTPSTLSVerifyClient, "https-listen-verify", false, "Verify client certificate provided to the HTTP listner")
	// timeouts
	flag.DurationVar(&cfg.flConnTimeout, "connect-timeout", time.Second, "timeout for establishing connection")
	flag.DurationVar(&cfg.flRPCTimeout, "rpc-timeout", time.Second, "timeout for health check rpc")
	// tls settings
	flag.BoolVar(&cfg.flTLS, "tls", false, "use TLS (default: false, INSECURE plaintext transport)")
	flag.BoolVar(&cfg.flTLSNoVerify, "tls-no-verify", false, "(with -tls) don't verify the certificate (INSECURE) presented by the server (default: false)")
	flag.StringVar(&cfg.flTLSCACert, "tls-ca-cert", "", "(with -tls, optional) file containing trusted certificates for verifying server")
	flag.StringVar(&cfg.flTLSClientCert, "tls-client-cert", "", "(with -tls, optional) client certificate for authenticating to the server (requires -tls-client-key)")
	flag.StringVar(&cfg.flTLSClientKey, "tls-client-key", "", "(with -tls) client private key for authenticating to the server (requires -tls-client-cert)")
	flag.StringVar(&cfg.flTLSServerName, "tls-server-name", "", "(with -tls) override the hostname used to verify the server certificate")
	flag.BoolVar(&cfg.flVerbose, "v", false, "verbose logs")

	flag.Parse()

	argError := func(s string, v ...interface{}) {
		log.Printf("error: "+s, v...)
		os.Exit(StatusInvalidArguments)
	}

	if cfg.flAddr == "" {
		argError("-addr not specified")
	}
	if cfg.flConnTimeout <= 0 {
		argError("-connect-timeout must be greater than zero (specified: %v)", cfg.flConnTimeout)
	}
	if cfg.flRPCTimeout <= 0 {
		argError("-rpc-timeout must be greater than zero (specified: %v)", cfg.flRPCTimeout)
	}
	if !cfg.flTLS && cfg.flTLSNoVerify {
		argError("specified -tls-no-verify without specifying -tls")
	}
	if !cfg.flTLS && cfg.flTLSCACert != "" {
		argError("specified -tls-ca-cert without specifying -tls")
	}
	if !cfg.flTLS && cfg.flTLSClientCert != "" {
		argError("specified -tls-client-cert without specifying -tls")
	}
	if !cfg.flTLS && cfg.flTLSServerName != "" {
		argError("specified -tls-server-name without specifying -tls")
	}
	if cfg.flTLSClientCert != "" && cfg.flTLSClientKey == "" {
		argError("specified -tls-client-cert without specifying -tls-client-key")
	}
	if cfg.flTLSClientCert == "" && cfg.flTLSClientKey != "" {
		argError("specified -tls-client-key without specifying -tls-client-cert")
	}
	if cfg.flTLSNoVerify && cfg.flTLSCACert != "" {
		argError("cannot specify -tls-ca-cert with -tls-no-verify (CA cert would not be used)")
	}
	if cfg.flTLSNoVerify && cfg.flTLSServerName != "" {
		argError("cannot specify -tls-server-name with -tls-no-verify (server name would not be used)")
	}
	if ((cfg.flHTTPSTLSServerCert != "" || cfg.flHTTPSTLSServerKey != "" || cfg.flHTTPSTLSVerifyClient ) &&  cfg.flHTTPListenAddr == "" ) {
		argError("cannot specify -https-listen-cert or https-listen-key if -http-listen-addr is not set (no https server would be listening)")
	}
	if cfg.flHTTPSTLSVerifyCA == "" && cfg.flHTTPSTLSVerifyClient {
		argError("cannot specify -https-listen-ca if https-listen-verify is set (you need a trust CA for client certificate https auth)")
	}

	if cfg.flVerbose {
		log.Printf("parsed options:")
		log.Printf("> addr=%s conn_timeout=%v rpc_timeout=%v", cfg.flAddr, cfg.flConnTimeout, cfg.flRPCTimeout)
		log.Printf("> tls=%v", cfg.flTLS)
		if cfg.flHTTPListenAddr != "" {
			log.Printf(" http-listen-addr=%v ", cfg.flHTTPListenAddr)
			log.Printf(" http-listen-path=%v ", cfg.flHTTPListenPath)
		}
		if cfg.flHTTPSTLSServerCert !="" {
			log.Printf(" https-listen-cert=%v ", cfg.flHTTPSTLSServerCert)
			log.Printf(" https-listen-key=%v ", cfg.flHTTPSTLSServerKey)
			log.Printf(" https-listen-verify=%v ", cfg.flHTTPSTLSVerifyClient)
			log.Printf(" https-listen-ca=%v ", cfg.flHTTPSTLSVerifyCA)
		}
		if cfg.flTLS {
			log.Printf("  > no-verify=%v ", cfg.flTLSNoVerify)
			log.Printf("  > ca-cert=%s", cfg.flTLSCACert)
			log.Printf("  > client-cert=%s", cfg.flTLSClientCert)
			log.Printf("  > client-key=%s", cfg.flTLSClientKey)
			log.Printf("  > server-name=%s", cfg.flTLSServerName)
		}
	}
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

func checkService(ctx context.Context) (healthpb.HealthCheckResponse_ServingStatus, error) {
	opts := []grpc.DialOption{
		grpc.WithUserAgent(cfg.flUserAgent),
		grpc.WithBlock()}
	if cfg.flTLS {
		creds, err := buildCredentials(cfg.flTLSNoVerify, cfg.flTLSCACert, cfg.flTLSClientCert, cfg.flTLSClientKey, cfg.flTLSServerName)
		if err != nil {
			log.Printf("failed to initialize tls credentials. error=%v", err)
			os.Exit(StatusInvalidArguments)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	if cfg.flVerbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	dialCtx, cancel2 := context.WithTimeout(ctx, cfg.flConnTimeout)
	defer cancel2()
	conn, err := grpc.DialContext(dialCtx, cfg.flAddr, opts...)
	if err != nil {
		if err == context.DeadlineExceeded {
			log.Printf("timeout: failed to connect service %q within %v", cfg.flAddr, cfg.flConnTimeout)
		} else {
			log.Printf("error: failed to connect service at %q: %+v", cfg.flAddr, err)
		}
		return healthpb.HealthCheckResponse_UNKNOWN, NewGrpcProbeError(StatusConnectionFailure, "StatusConnectionFailure")
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if cfg.flVerbose {
		log.Printf("connection established (took %v)", connDuration)
	}

	rpcStart := time.Now()
	rpcCtx, rpcCancel := context.WithTimeout(ctx, cfg.flRPCTimeout)
	defer rpcCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx, &healthpb.HealthCheckRequest{Service: cfg.flService})
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			log.Printf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health)")
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			log.Printf("timeout: health rpc did not complete within %v", cfg.flRPCTimeout)
		} else {
			log.Printf("error: health rpc failed: %+v", err)
		}
		return healthpb.HealthCheckResponse_UNKNOWN, NewGrpcProbeError(StatusRPCFailure, "StatusRPCFailure")
	}
	rpcDuration := time.Since(rpcStart)

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		log.Printf("service unhealthy (responded with %q)", resp.GetStatus().String())
		return healthpb.HealthCheckResponse_NOT_SERVING, NewGrpcProbeError(StatusUnhealthy, "StatusUnhealthy")
	}
	if cfg.flVerbose {
		log.Printf("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}
	return resp.GetStatus(), nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	resp, err := checkService(r.Context())
	if err != nil {
        if pe, ok := err.(*GrpcProbeError); ok {
			log.Printf("HealtCheck Probe Error: %v", pe.Error())
			switch pe.Code {
			case StatusUnhealthy:
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
			case StatusConnectionFailure:
				http.Error(w, err.Error(), http.StatusBadGateway)
			case StatusRPCFailure:
				http.Error(w, err.Error(), http.StatusBadGateway)
			default:
				http.Error(w, err.Error(), http.StatusBadGateway)
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	}
	if cfg.flVerbose {
		log.Printf("status: %v", resp.String())
	}
	fmt.Fprintf(w, "status: %v", resp.String())
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
			os.Exit(0)
		}
	}()

	if (cfg.flHTTPListenAddr != "") {
		tlsConfig := &tls.Config{}
		if (cfg.flHTTPSTLSVerifyClient) {
			caCert, err := ioutil.ReadFile(cfg.flHTTPSTLSVerifyCA)
			if err != nil {
				log.Fatal(err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)

			tlsConfig = &tls.Config{
				ClientCAs: caCertPool,
				ClientAuth: tls.RequireAndVerifyClientCert,
			}
		}
		tlsConfig.BuildNameToCertificate()

		srv := &http.Server{
			Addr: cfg.flHTTPListenAddr,
			TLSConfig: tlsConfig,
		}
		http2.ConfigureServer(srv, &http2.Server{})
		http.HandleFunc(cfg.flHTTPListenPath, healthHandler)

		var err error
		if (cfg.flHTTPSTLSServerCert != "" && cfg.flHTTPSTLSServerKey != "" ) {
			err = srv.ListenAndServeTLS(cfg.flHTTPSTLSServerCert, cfg.flHTTPSTLSServerKey)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil {
			log.Fatal("ListenAndServe Error: ", err)
		}
	}
	resp, err := checkService(ctx)
	if err != nil {
        if pe, ok := err.(*GrpcProbeError); ok {
			log.Printf("HealtCheck Probe Error: %v", pe.Error())
			os.Exit(pe.Code)
		}
		os.Exit(1)
	}
	log.Printf("status: %v", resp.String())
}
