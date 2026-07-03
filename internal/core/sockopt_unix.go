//go:build linux || darwin || freebsd || openbsd || netbsd

package core

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// setReusePort sets SO_REUSEADDR and SO_REUSEPORT on the raw socket so the
// server can be restarted immediately without "address already in use" errors.
func setReusePort(network, address string, c syscall.RawConn) error {
	var setErr error
	err := c.Control(func(fd uintptr) {
		if e := unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); e != nil {
			setErr = e
			return
		}
		// SO_REUSEPORT is not universally available; ignore ENOPROTOOPT.
		_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return setErr
}
