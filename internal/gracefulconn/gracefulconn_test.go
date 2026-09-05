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

package gracefulconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestClose_UnreadDataIsNotAReset is the graceful counterpart of closing with
// unread data in the receive buffer: the server must read EOF, not a reset.
func TestClose_UnreadDataIsNotAReset(t *testing.T) {
	ln := listenLoopback(t)
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		if _, err := c.Write([]byte("unread by the client")); err != nil {
			serverErr <- fmt.Errorf("write: %w", err)
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Read(make([]byte, 16))
		serverErr <- err
	}()

	raw := dialLoopback(t, ln.Addr().String())
	waitReadable(t, raw)
	start := time.Now()
	if err := Wrap(raw, 2*time.Second).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Close took %v; should return as soon as the peer closes, not at the drain deadline", elapsed)
	}
	if err := <-serverErr; !errors.Is(err, io.EOF) {
		t.Fatalf("server read after graceful close: got %v, want EOF", err)
	}
}

// TestClose_DrainsDataSentAfterOurFIN covers a peer that still has something to
// say after seeing our FIN, like a server replying to GOAWAY or close_notify.
// The reply must be drained, not answered with RST.
func TestClose_DrainsDataSentAfterOurFIN(t *testing.T) {
	ln := listenLoopback(t)
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		buf := make([]byte, 16)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(buf); !errors.Is(err, io.EOF) {
			serverErr <- fmt.Errorf("first read: got %v, want EOF", err)
			return
		}
		if _, err := c.Write([]byte("late reply")); err != nil {
			serverErr <- fmt.Errorf("late write: %w", err)
			return
		}
		serverErr <- awaitSocketError(c, 200*time.Millisecond)
	}()

	raw := dialLoopback(t, ln.Addr().String())
	if err := Wrap(raw, 2*time.Second).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server socket error after writing to a half-closed peer: got %v, want none", err)
	}
}

func TestClose_PeerNeverClosesIsBounded(t *testing.T) {
	ln := listenLoopback(t)
	release := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("hello"))
		<-release
	}()
	defer close(release)

	raw := dialLoopback(t, ln.Addr().String())
	const drain = 200 * time.Millisecond
	start := time.Now()
	if err := Wrap(raw, drain).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < drain || elapsed > drain+time.Second {
		t.Fatalf("Close took %v, want about %v", elapsed, drain)
	}
}

// TestClose_WithConcurrentReader mimics grpc-go, whose reader goroutine is
// blocked in Read on the connection while Close runs.
func TestClose_WithConcurrentReader(t *testing.T) {
	ln := listenLoopback(t)
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		if _, err := c.Write([]byte("response")); err != nil {
			serverErr <- err
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Read(make([]byte, 16))
		serverErr <- err
	}()

	wrapped := Wrap(dialLoopback(t, ln.Addr().String()), 2*time.Second)
	readerDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := wrapped.Read(buf); err != nil {
				readerDone <- err
				return
			}
		}
	}()

	if err := wrapped.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readerDone:
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("reader goroutine ended with %v, want EOF, closed, or deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader goroutine did not exit after Close")
	}
	if err := <-serverErr; !errors.Is(err, io.EOF) {
		t.Fatalf("server read: got %v, want EOF", err)
	}
}

func TestClose_WithoutCloseWrite(t *testing.T) {
	p1, p2 := net.Pipe()
	start := time.Now()
	if err := Wrap(p1, time.Second).Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Close took %v on a conn without CloseWrite; want immediate", elapsed)
	}
	if _, err := p2.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("peer read: got %v, want EOF", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	ln := listenLoopback(t)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	wrapped := Wrap(dialLoopback(t, ln.Addr().String()), time.Second)
	first := wrapped.Close()
	start := time.Now()
	second := wrapped.Close()
	if first != nil || second != nil {
		t.Fatalf("Close errors: first=%v second=%v", first, second)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("second Close took %v; want immediate", elapsed)
	}
}

func TestParseAddr(t *testing.T) {
	for _, tc := range []struct{ in, network, address string }{
		{"127.0.0.1:1", "tcp", "127.0.0.1:1"},
		{"[::1]:1", "tcp", "[::1]:1"},
		{"example.com:443", "tcp", "example.com:443"},
		{"unix:///tmp/s.sock", "unix", "/tmp/s.sock"},
		{"unix:rel/s.sock", "unix", "rel/s.sock"},
		{"\x00abstract", "unix", "\x00abstract"},
	} {
		network, address := parseAddr(tc.in)
		if network != tc.network || address != tc.address {
			t.Errorf("parseAddr(%q) = (%q, %q), want (%q, %q)", tc.in, network, address, tc.network, tc.address)
		}
	}
}

func TestDialer_TCP(t *testing.T) {
	ln := listenLoopback(t)
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Read(make([]byte, 16))
		serverErr <- err
	}()
	c, err := Dialer(time.Second)(context.Background(), ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-serverErr; !errors.Is(err, io.EOF) {
		t.Fatalf("server read: got %v, want EOF", err)
	}
}

func TestDialer_Unix(t *testing.T) {
	dir, err := os.MkdirTemp("", "gc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	if len(path) > 100 {
		t.Skipf("socket path %q too long for sockaddr_un", path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	serverErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Read(make([]byte, 16))
		serverErr <- err
	}()
	// grpc-go hands "unix://" + absolute path to a custom dialer.
	c, err := Dialer(time.Second)(context.Background(), "unix://"+path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-serverErr; !errors.Is(err, io.EOF) {
		t.Fatalf("server read: got %v, want EOF", err)
	}
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func dialLoopback(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return c.(*net.TCPConn)
}

// waitReadable blocks until c's receive buffer holds data, without consuming it.
func waitReadable(t *testing.T, c *net.TCPConn) {
	t.Helper()
	rc, err := c.SyscallConn()
	if err != nil {
		t.Fatalf("syscall conn: %v", err)
	}
	buf := make([]byte, 1)
	err = rc.Read(func(fd uintptr) bool {
		_, _, err := syscall.Recvfrom(int(fd), buf, syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		return !errors.Is(err, syscall.EAGAIN)
	})
	if err != nil {
		t.Fatalf("waiting for readable: %v", err)
	}
}

// awaitSocketError polls SO_ERROR on c until the kernel reports an error or
// the timeout passes, returning nil in the latter case.
func awaitSocketError(c net.Conn, timeout time.Duration) error {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return nil
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var v int
		var gerr error
		if cerr := rc.Control(func(fd uintptr) {
			v, gerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_ERROR)
		}); cerr != nil {
			return cerr
		}
		if gerr != nil {
			return gerr
		}
		if v != 0 {
			return syscall.Errno(v)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}
