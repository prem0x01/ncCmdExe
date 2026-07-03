//go:build windows

package core

import "syscall"

// setReusePort is a no-op on Windows; SO_REUSEADDR semantics differ and
// net.Listen already sets the equivalent option.
func setReusePort(network, address string, c syscall.RawConn) error {
	return nil
}
