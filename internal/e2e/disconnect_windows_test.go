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

package e2e

import (
	"errors"
	"net"
	"syscall"
)

// soError is SO_ERROR in winsock2.h, which package syscall doesn't define.
const soError = 0x1007

// isReset reports whether err is what a server observes when the peer answered
// with a TCP RST. Winsock reports a reset as WSAECONNRESET on reads, and as
// WSAECONNABORTED when it aborts the connection on a later write.
func isReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED)
}

// pendingSocketError returns the error queued on c (SO_ERROR), or nil, like
// its Unix counterpart.
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
		v, err = syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, soError)
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
