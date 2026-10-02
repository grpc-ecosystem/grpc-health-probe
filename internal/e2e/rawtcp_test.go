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
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// These tests document, at the socket level and independently of gRPC, the two
// kernel behaviours behind grpc-ecosystem/grpc-health-probe#34. They are
// controls: they pass on unmodified kernels and show what a plain close() does.

// TestRawTCP_CloseWithUnreadData_PeerSeesReset: on Linux, close() on a socket
// whose receive buffer still holds unread data sends RST instead of FIN.
func TestRawTCP_CloseWithUnreadData_PeerSeesReset(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RST on close-with-unread-data is Linux tcp_close behaviour; BSD-derived kernels flush and send FIN")
	}
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

	client := dialLoopback(t, ln.Addr().String())
	waitReadable(t, client)
	if err := client.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}
	if err := <-serverErr; !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("server read after client close() with unread data: got %v, want ECONNRESET", err)
	}
}

// TestRawTCP_DataAfterClose_PeerSeesReset: on every OS, data arriving at a
// socket that has been fully closed is answered with RST. This is what happens
// when a server replies to the client's GOAWAY or TLS close_notify after the
// client has already called close().
func TestRawTCP_DataAfterClose_PeerSeesReset(t *testing.T) {
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
		serverErr <- awaitSocketError(c, 2*time.Second)
	}()

	client := dialLoopback(t, ln.Addr().String())
	if err := client.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}
	if err := <-serverErr; !isReset(err) {
		t.Fatalf("server socket error after writing to a closed peer: got %v, want ECONNRESET or EPIPE", err)
	}
}

// awaitSocketError polls SO_ERROR on c until the kernel reports an error or
// the timeout passes, returning nil in the latter case. Reading would not do:
// once the peer's FIN has been delivered, read() returns EOF and never
// surfaces a reset that arrives afterwards.
func awaitSocketError(c net.Conn, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := pendingSocketError(c); err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
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
