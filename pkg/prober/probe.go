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
package prober

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	// StatusInvalidArguments indicates specified invalid arguments.
	StatusInvalidArguments = iota
	// StatusConnectionFailure indicates connection failed.
	StatusConnectionFailure
	// StatusRPCFailure indicates rpc failed.
	StatusRPCFailure
	// StatusUnhealthy indicates rpc succeeded but indicates unhealthy service.
	StatusUnhealthy
)

type Config struct {
	Addr          string
	Service       string
	UserAgent     string
	ConnTimeout   time.Duration
	RPCTimeout    time.Duration
	TLS           bool
	TLSNoVerify   bool
	TLSCACert     string
	TLSClientCert string
	TLSClientKey  string
	TLSServerName string
	Verbose       bool
}

func (c *Config) Validate() error {
	if c.Addr == "" {
		return errors.New("-addr not specified")
	}
	if c.ConnTimeout <= 0 {
		return fmt.Errorf("-connect-timeout must be greater than zero (specified: %v)", c.ConnTimeout)
	}
	if c.RPCTimeout <= 0 {
		return fmt.Errorf("-rpc-timeout must be greater than zero (specified: %v)", c.RPCTimeout)
	}
	if !c.TLS && c.TLSNoVerify {
		return errors.New("specified -tls-no-verify without specifying -tls")
	}
	if !c.TLS && c.TLSCACert != "" {
		return errors.New("specified -tls-ca-cert without specifying -tls")
	}
	if !c.TLS && c.TLSClientCert != "" {
		return errors.New("specified -tls-client-cert without specifying -tls")
	}
	if !c.TLS && c.TLSServerName != "" {
		return errors.New("specified -tls-server-name without specifying -tls")
	}
	if c.TLSClientCert != "" && c.TLSClientKey == "" {
		return errors.New("specified -tls-client-cert without specifying -tls-client-key")
	}
	if c.TLSClientCert == "" && c.TLSClientKey != "" {
		return errors.New("specified -tls-client-key without specifying -tls-client-cert")
	}
	if c.TLSNoVerify && c.TLSCACert != "" {
		return errors.New("cannot specify -tls-ca-cert with -tls-no-verify (CA cert would not be used)")
	}
	if c.TLSNoVerify && c.TLSServerName != "" {
		return errors.New("cannot specify -tls-server-name with -tls-no-verify (server name would not be used)")
	}

	return nil
}

type Error struct {
	Err      string
	ExitCode int
}

func (e Error) Error() string {
	return e.Err
}

type logger interface {
	Printf(format string, v ...interface{})
}

type checker struct {
	config *Config
	logger logger
}

func NewChecker(config *Config, l logger) (*checker, *Error) {
	if err := config.Validate(); err != nil {
		return nil, &Error{err.Error(), StatusInvalidArguments}
	}
	return &checker{config, l}, nil
}

func (c *checker) Check(ctx context.Context) (*healthpb.HealthCheckResponse, *Error) {
	opts := []grpc.DialOption{
		grpc.WithUserAgent(c.config.UserAgent),
		grpc.WithBlock()}
	if c.config.TLS {
		creds, err := buildCredentials(c.config.TLSNoVerify, c.config.TLSCACert, c.config.TLSClientCert, c.config.TLSClientKey, c.config.TLSServerName)
		if err != nil {
			return nil, &Error{fmt.Sprintf("failed to initialize tls credentials. error=%v", err), StatusInvalidArguments}
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	if c.config.Verbose {
		log.Print("establishing connection")
	}
	connStart := time.Now()
	dialCtx, cancel2 := context.WithTimeout(ctx, c.config.ConnTimeout)
	defer cancel2()
	conn, err := grpc.DialContext(dialCtx, c.config.Addr, opts...)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil, &Error{fmt.Sprintf("timeout: failed to connect service %q within %v", c.config.Addr, c.config.ConnTimeout), StatusConnectionFailure}
		} else {
			return nil, &Error{fmt.Sprintf("error: failed to connect service at %q: %+v", c.config.Addr, err), StatusConnectionFailure}
		}
	}
	connDuration := time.Since(connStart)
	defer conn.Close()
	if c.config.Verbose {
		c.log("connection established (took %v)", connDuration)
	}

	rpcStart := time.Now()
	rpcCtx, rpcCancel := context.WithTimeout(ctx, c.config.RPCTimeout)
	defer rpcCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(rpcCtx, &healthpb.HealthCheckRequest{Service: c.config.Service})
	if err != nil {
		if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
			return nil, &Error{fmt.Sprintf("error: this server does not implement the grpc health protocol (grpc.health.v1.Health)"), StatusRPCFailure}
		} else if stat, ok := status.FromError(err); ok && stat.Code() == codes.DeadlineExceeded {
			return nil, &Error{fmt.Sprintf("timeout: health rpc did not complete within %v", c.config.RPCTimeout), StatusRPCFailure}
		} else {
			return nil, &Error{fmt.Sprintf("error: health rpc failed: %+v", err), StatusRPCFailure}
		}
	}
	rpcDuration := time.Since(rpcStart)

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return nil, &Error{fmt.Sprintf("service unhealthy (responded with %q)", resp.GetStatus().String()), StatusUnhealthy}
	}
	if c.config.Verbose {
		c.log("time elapsed: connect=%v rpc=%v", connDuration, rpcDuration)
	}

	return resp, nil
}

func (c *checker) log(format string, v ...interface{}) {
	if c.logger != nil {
		c.logger.Printf(format, v...)
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
