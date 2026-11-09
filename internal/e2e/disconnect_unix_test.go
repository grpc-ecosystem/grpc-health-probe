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
	"net"
	"syscall"
)

// isReset reports whether err is what a server observes when the peer answered
// with a TCP RST. Linux records EPIPE instead of ECONNRESET when the RST
// arrives after the server has already seen the peer's FIN.
func isReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

// pendingSocketError returns the error the kernel has queued on c (SO_ERROR),
// or nil. It is the one way to observe an RST that arrived after the peer's
// FIN: once FIN has been delivered, read() returns EOF without reporting it.
func pendingSocketError(c net.Conn) error {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return nil
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var v int
	if cerr := rc.Control(func(fd uintptr) {
		v, err = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_ERROR)
	}); cerr != nil {
		return cerr
	}
	if err != nil {
		return err
	}
	if v == 0 {
		return nil
	}
	return syscall.Errno(v)
}
