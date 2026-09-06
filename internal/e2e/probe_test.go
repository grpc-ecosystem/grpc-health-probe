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
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Exit codes documented in README.md.
const (
	exitInvalidArguments  = 1
	exitConnectionFailure = 2
	exitRPCFailure        = 3
	exitUnhealthy         = 4
)

func TestServing(t *testing.T) {
	srv := startHealthServer(t, serverConfig{})
	code, out := runProbe(t, "-addr", srv.addr)
	if code != 0 || !strings.Contains(out, "status: SERVING") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestServiceStatuses(t *testing.T) {
	h := health.NewServer()
	h.SetServingStatus("svc-ok", healthpb.HealthCheckResponse_SERVING)
	h.SetServingStatus("svc-down", healthpb.HealthCheckResponse_NOT_SERVING)
	srv := startHealthServer(t, serverConfig{handler: h})

	for _, tc := range []struct {
		service string
		code    int
		want    string
	}{
		{service: "svc-ok", code: 0, want: "status: SERVING"},
		{service: "svc-down", code: exitUnhealthy, want: "service unhealthy"},
		{service: "svc-missing", code: exitRPCFailure, want: "health rpc failed"},
	} {
		code, out := runProbe(t, "-addr", srv.addr, "-service", tc.service)
		if code != tc.code || !strings.Contains(out, tc.want) {
			t.Errorf("-service %s: exit %d, want %d with %q; output:\n%s", tc.service, code, tc.code, tc.want, out)
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: nil, want: "-addr not specified"},
		{args: []string{"-addr", "x:1", "-connect-timeout", "0"}, want: "-connect-timeout must be greater than zero"},
		{args: []string{"-addr", "x:1", "-rpc-timeout", "-1s"}, want: "-rpc-timeout must be greater than zero"},
		{args: []string{"-addr", "x:1", "-tls-no-verify"}, want: "specified -tls-no-verify without specifying -tls"},
		{args: []string{"-addr", "x:1", "-tls-ca-cert", "ca.pem"}, want: "specified -tls-ca-cert without specifying -tls"},
		{args: []string{"-addr", "x:1", "-tls", "-tls-client-cert", "c.pem"}, want: "specified -tls-client-cert without specifying -tls-client-key"},
		{args: []string{"-addr", "x:1", "-tls", "-tls-client-key", "k.pem"}, want: "specified -tls-client-key without specifying -tls-client-cert"},
		{args: []string{"-addr", "x:1", "-tls", "-tls-no-verify", "-tls-ca-cert", "ca.pem"}, want: "cannot specify -tls-ca-cert with -tls-no-verify"},
		{args: []string{"-addr", "x:1", "-tls", "-tls-no-verify", "-tls-server-name", "n"}, want: "cannot specify -tls-server-name with -tls-no-verify"},
		{args: []string{"-addr", "x:1", "-alts", "-spiffe"}, want: "-alts and -spiffe are mutually incompatible"},
		{args: []string{"-addr", "x:1", "-tls", "-alts"}, want: "cannot specify -tls with -alts"},
		{args: []string{"-addr", "x:1", "-tls", "-spiffe"}, want: "-tls and -spiffe are mutually incompatible"},
		{args: []string{"-addr", "x:1", "-rpc-header", "novalue"}, want: "invalid RPC header"},
		{args: []string{"-addr", "x:1", "-no-such-flag"}, want: "flag provided but not defined"},
	} {
		code, out := runProbe(t, tc.args...)
		if code != exitInvalidArguments || !strings.Contains(out, tc.want) {
			t.Errorf("%q: exit %d, want %d with %q; output:\n%s", tc.args, code, exitInvalidArguments, tc.want, out)
		}
	}
}

func TestVersion(t *testing.T) {
	code, out := runProbe(t, "-version")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

func TestConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	code, out := runProbe(t, "-addr", addr, "-connect-timeout", "500ms")
	if code != exitConnectionFailure || !strings.Contains(out, "failed to connect") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestConnectTimeout(t *testing.T) {
	// A listener that never speaks HTTP/2: the TCP handshake succeeds but the
	// gRPC connection never becomes ready.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	code, out := runProbe(t, "-addr", ln.Addr().String(), "-connect-timeout", "300ms")
	if code != exitConnectionFailure || !strings.Contains(out, "timeout: failed to connect") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestRPCTimeout(t *testing.T) {
	srv := startHealthServer(t, serverConfig{handler: &recordingHealth{block: true}})
	code, out := runProbe(t, "-addr", srv.addr, "-rpc-timeout", "300ms")
	if code != exitRPCFailure || !strings.Contains(out, "timeout: health rpc did not complete") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestRPCHeadersAndUserAgent(t *testing.T) {
	h := &recordingHealth{}
	srv := startHealthServer(t, serverConfig{handler: h})
	code, out := runProbe(t, "-addr", srv.addr,
		"-rpc-header", "foo: bar", "-rpc-header", "foo:baz", "-rpc-header", "x-other: 1",
		"-user-agent", "probe-e2e/1.0")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	md := h.lastMetadata()
	if got := md.Get("foo"); !reflect.DeepEqual(got, []string{"bar", "baz"}) {
		t.Errorf("foo = %q, want [bar baz]", got)
	}
	if got := md.Get("x-other"); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("x-other = %q, want [1]", got)
	}
	if got := md.Get("user-agent"); len(got) != 1 || !strings.HasPrefix(got[0], "probe-e2e/1.0") {
		t.Errorf("user-agent = %q, want prefix probe-e2e/1.0", got)
	}
}

func TestTLS(t *testing.T) {
	srv := startHealthServer(t, serverConfig{tls: true})

	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "no verify", args: []string{"-tls", "-tls-no-verify"}, code: 0, want: "status: SERVING"},
		{name: "ca cert", args: []string{"-tls", "-tls-ca-cert", srv.certFile}, code: 0, want: "status: SERVING"},
		{name: "untrusted cert", args: []string{"-tls", "-connect-timeout", "500ms"}, code: exitConnectionFailure, want: "failed to connect"},
		{name: "plaintext to tls server", args: []string{"-connect-timeout", "500ms"}, code: exitConnectionFailure, want: "failed to connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runProbe(t, append([]string{"-addr", srv.addr}, tc.args...)...)
			if code != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want %d with %q; output:\n%s", code, tc.code, tc.want, out)
			}
		})
	}
}

func TestUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "probe.sock")
	if len(path) > 100 {
		t.Skipf("socket path %q too long for sockaddr_un", path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	startHealthServer(t, serverConfig{listener: ln})
	code, out := runProbe(t, "-addr", "unix://"+path)
	if code != 0 || !strings.Contains(out, "status: SERVING") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestVerbose(t *testing.T) {
	srv := startHealthServer(t, serverConfig{})
	code, out := runProbe(t, "-addr", srv.addr, "-v")
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	for _, want := range []string{"parsed options:", "establishing connection", "connection established", "time elapsed:", "status: SERVING"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output lacks %q:\n%s", want, out)
		}
	}
}
