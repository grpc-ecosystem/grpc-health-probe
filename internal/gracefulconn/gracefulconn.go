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

// Package gracefulconn provides a net.Conn whose Close ends the TCP
// connection with a FIN/ACK exchange instead of a reset.
//
// A plain close() makes the kernel send RST when the receive buffer still
// holds unread data (Linux), or when the peer sends anything after the socket
// is gone (every OS). Both happen at the end of a health check: the server
// answers the client's GOAWAY or TLS close_notify, and the client has already
// closed. grpc-go's ClientConn.Close does a hard close of the socket and offers
// no hook to change that, but grpc.WithContextDialer lets the caller own the
// net.Conn. This package half-closes the socket (sending FIN), drains whatever
// the peer still sends until its FIN arrives, and only then closes.
//
// See https://github.com/grpc-ecosystem/grpc-health-probe/issues/34.
package gracefulconn

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// DefaultDrainTimeout bounds how long Close waits for the peer's FIN after
// sending ours. A well-behaved server closes as soon as it reads EOF, so the
// wait is one round trip. The bound only matters for a peer that never closes.
const DefaultDrainTimeout = 500 * time.Millisecond

// Dialer returns a dial function for grpc.WithContextDialer whose connections
// close gracefully. It accepts the address forms grpc-go hands to a custom
// dialer: "host:port", "unix://absolute-path", "unix:relative-path", and an
// abstract socket name starting with a NUL byte.
func Dialer(drainTimeout time.Duration) func(ctx context.Context, addr string) (net.Conn, error) {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		network, address := parseAddr(addr)
		c, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return Wrap(c, drainTimeout), nil
	}
}

// Wrap returns c with a graceful Close. Connections without CloseWrite (for
// example net.Pipe) are closed normally.
func Wrap(c net.Conn, drainTimeout time.Duration) net.Conn {
	return &conn{Conn: c, drainTimeout: drainTimeout}
}

func parseAddr(addr string) (network, address string) {
	switch {
	case strings.HasPrefix(addr, "unix://"):
		return "unix", strings.TrimPrefix(addr, "unix://")
	case strings.HasPrefix(addr, "unix:"):
		return "unix", strings.TrimPrefix(addr, "unix:")
	case strings.HasPrefix(addr, "\x00"):
		return "unix", addr
	}
	return "tcp", addr
}

type conn struct {
	net.Conn
	drainTimeout time.Duration

	once     sync.Once
	closeErr error
}

type closeWriter interface {
	CloseWrite() error
}

// Close half-closes the connection, drains it until the peer closes its side
// or drainTimeout passes, then closes the socket. It is safe to call while
// another goroutine is blocked in Read on the same connection: both readers
// observe the peer's EOF, and the final close unblocks whatever remains.
func (c *conn) Close() error {
	c.once.Do(func() {
		if cw, ok := c.Conn.(closeWriter); ok {
			// A failed CloseWrite means the socket is already dead; skip the wait.
			if err := cw.CloseWrite(); err == nil {
				_ = c.Conn.SetReadDeadline(time.Now().Add(c.drainTimeout))
				_, _ = io.Copy(io.Discard, c.Conn)
			}
		}
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}
