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

//go:build unix || windows

package e2e

import (
	"errors"
	"flag"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Whether a given disconnect ends in a reset depends on timing, so the test
// probes several times. Ten runs keep CI fast and are enough to catch a
// regression; when investigating, or to gain confidence that a change really
// leaves no resets behind, pass -disconnect-runs=100 or more (each run takes
// about 0.2s).
var disconnectRuns = flag.Int("disconnect-runs", 10, "probe runs per case in TestProbeDisconnectsGracefully")

// TestProbeDisconnectsGracefully runs the probe against a health server and
// checks how the server experiences the probe's disconnect. A well-behaved
// client ends the TCP connection with a FIN; the server then reads EOF. A
// client that closes its socket while the server still has something to say
// (a GOAWAY or TLS close_notify reply) makes the kernel answer with RST, which
// the server sees as "connection reset by peer" and logs on every probe.
// See grpc-ecosystem/grpc-health-probe#34.
func TestProbeDisconnectsGracefully(t *testing.T) {
	runs := *disconnectRuns
	for _, tc := range []struct {
		name string
		tls  bool
	}{
		{name: "plaintext", tls: false},
		{name: "tls", tls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disconnects := make(chan error, runs)
			srv := startHealthServer(t, serverConfig{
				tls: tc.tls,
				wrapListener: func(ln net.Listener) net.Listener {
					return &recordingListener{Listener: ln, disconnects: disconnects}
				},
			})
			args := []string{"-addr", srv.addr}
			if tc.tls {
				args = append(args, "-tls", "-tls-no-verify")
			}
			resets := 0
			for i := 0; i < runs; i++ {
				code, out := runProbe(t, args...)
				if code != 0 || !strings.Contains(out, "status: SERVING") {
					t.Fatalf("run %d (after %d resets): exit %d, output:\n%s", i, resets, code, out)
				}
				select {
				case err := <-disconnects:
					switch {
					case isReset(err):
						resets++
						t.Logf("run %d: server saw a reset on probe disconnect: %v", i, err)
					case !errors.Is(err, io.EOF):
						// Neither a FIN nor a reset, e.g. the linger timed out.
						t.Errorf("run %d: server saw no clean FIN on probe disconnect: %v", i, err)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("run %d (after %d resets): server never closed the probe's connection", i, resets)
				}
			}
			t.Logf("%d of %d probe disconnects ended in a reset", resets, runs)
			if resets > 0 {
				t.Errorf("server saw a TCP reset on %d of %d probe disconnects; want a clean FIN every time", resets, runs)
			}
		})
	}
}

// recordingListener wraps accepted connections in recordingConn.
type recordingListener struct {
	net.Listener
	disconnects chan<- error
}

func (l *recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &recordingConn{Conn: c, disconnects: l.disconnects}, nil
}

// lingerRead bounds how long a closing server connection keeps reading.
const lingerRead = 500 * time.Millisecond

// lingerAfterEOF is how long, after reading the peer's FIN, the server keeps
// watching the socket for a reset caused by its own final write.
const lingerAfterEOF = 100 * time.Millisecond

// recordingConn behaves like a server that keeps its read loop running for a
// moment after deciding to close the connection, the way netty does. What it
// observes (EOF, a timeout, or a reset) is sent on disconnects.
type recordingConn struct {
	net.Conn
	disconnects chan<- error
	closed      bool

	mu sync.Mutex
	// reset is the first reset the server's own reads and writes returned.
	// Once a read has reported it, the socket reads EOF and SO_ERROR is
	// clear, so linger alone would take the disconnect for a clean one.
	reset error
}

func (c *recordingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.noteReset(err)
	return n, err
}

func (c *recordingConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.noteReset(err)
	return n, err
}

func (c *recordingConn) noteReset(err error) {
	if !isReset(err) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reset == nil {
		c.reset = err
	}
}

func (c *recordingConn) Close() error {
	if !c.closed {
		c.closed = true
		err := c.linger()
		c.mu.Lock()
		if c.reset != nil {
			err = c.reset
		}
		c.mu.Unlock()
		c.disconnects <- err
	}
	return c.Conn.Close()
}

// linger reads until an error. On EOF it keeps watching the socket for a
// short while, because a reset triggered by our own last write (GOAWAY, TLS
// close_notify) arrives after the peer's FIN has already been delivered.
func (c *recordingConn) linger() error {
	_ = c.Conn.SetReadDeadline(time.Now().Add(lingerRead))
	buf := make([]byte, 4096)
	for {
		_, err := c.Conn.Read(buf)
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		deadline := time.Now().Add(lingerAfterEOF)
		for time.Now().Before(deadline) {
			if perr := pendingSocketError(c.Conn); perr != nil {
				return perr
			}
			time.Sleep(5 * time.Millisecond)
		}
		return err
	}
}
