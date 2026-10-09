// Copyright 2026 Google LLC
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

//go:build unix

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// probeBin is the path of the grpc-health-probe binary built in TestMain.
var probeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "grpc-health-probe-e2e")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	probeBin = filepath.Join(dir, "grpc-health-probe")
	buildArgs := []string{"build", "-o", probeBin}
	if os.Getenv("GOCOVERDIR") != "" {
		// Instrument the probe so that every run writes coverage data to
		// GOCOVERDIR, which the probe inherits through the environment.
		// Under "go test -cover" the variable points at go test's own
		// temporary directory, whose foreign data files are ignored and
		// deleted; to see the probe's coverage, run this package without
		// -cover and with GOCOVERDIR set, then use "go tool covdata".
		buildArgs = append(buildArgs, "-cover")
	}
	build := exec.Command("go", append(buildArgs, "../..")...)
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building grpc-health-probe: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// runProbe runs the probe binary and returns its exit code and combined output.
func runProbe(t *testing.T, args ...string) (code int, output string) {
	t.Helper()
	out, err := exec.Command(probeBin, args...).CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running probe: %v", err)
	return -1, ""
}

type serverConfig struct {
	// tls serves with a self-signed certificate for 127.0.0.1.
	tls bool
	// handler overrides the default health service, which reports the empty
	// service name as SERVING.
	handler healthpb.HealthServer
	// listener overrides the default loopback TCP listener.
	listener net.Listener
}

type server struct {
	addr string
	// certFile holds the PEM-encoded server certificate when tls is enabled,
	// usable as -tls-ca-cert.
	certFile string
}

// startHealthServer serves grpc.health.v1.Health on a loopback port (or the
// configured listener) for the duration of the test.
func startHealthServer(t *testing.T, cfg serverConfig) *server {
	t.Helper()
	ln := cfg.listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
	}
	srv := &server{addr: ln.Addr().String()}

	var opts []grpc.ServerOption
	if cfg.tls {
		cert, certPEM := selfSignedCert(t)
		srv.certFile = filepath.Join(t.TempDir(), "server.crt")
		if err := os.WriteFile(srv.certFile, certPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})))
	}
	s := grpc.NewServer(opts...)
	handler := cfg.handler
	if handler == nil {
		h := health.NewServer()
		h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		handler = h
	}
	healthpb.RegisterHealthServer(s, handler)
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Stop)
	return srv
}

// recordingHealth answers SERVING and remembers the metadata of the last
// Check. With block set, it holds the RPC until the client gives up.
type recordingHealth struct {
	healthpb.UnimplementedHealthServer
	block bool

	mu sync.Mutex
	md metadata.MD
}

func (h *recordingHealth) Check(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	h.mu.Lock()
	h.md = md
	h.mu.Unlock()
	if h.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
}

func (h *recordingHealth) lastMetadata() metadata.MD {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.md
}

// selfSignedCert returns a certificate for 127.0.0.1 and its PEM encoding.
func selfSignedCert(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "grpc-health-probe e2e"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, certPEM
}
