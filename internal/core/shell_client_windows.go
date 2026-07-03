//go:build windows

package core

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/term"

	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// remoteShellClient connects to a remoteShellServer on Windows.
// No PTY is available, so this provides a plain framed pipe session.
func remoteShellClient(ctx context.Context, conn net.Conn) {
	// Read server's OS/shell banner.
	msgType, payload, err := protocol.ReadPacket(conn)
	if err != nil {
		fmt.Fprintln(os.Stderr, cliErr.Render("connect: "+err.Error()))
		return
	}
	if msgType == protocol.MsgSysInfo {
		fmt.Println(clientStyle.Render("  Remote → "+string(payload)) +
			"\r\n" + cliInfo.Render("  Press Ctrl+] to detach\r\n"))
	}

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err == nil {
			defer func() {
				term.Restore(fd, oldState) //nolint:errcheck
				fmt.Print("\r\n")
			}()
		}
	}

	// stdin → MsgData; Ctrl+] detaches.
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				for _, b := range buf[:n] {
					if b == 0x1d { // Ctrl+]
						conn.Close()
						return
					}
				}
				if e := protocol.SendPacket(conn, protocol.MsgData, buf[:n]); e != nil {
					return
				}
			}
			if err != nil {
				conn.Close()
				return
			}
		}
	}()

	// MsgData → stdout.
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return
		}
		if msgType == protocol.MsgData {
			os.Stdout.Write(payload) //nolint:errcheck
		}
	}
}
