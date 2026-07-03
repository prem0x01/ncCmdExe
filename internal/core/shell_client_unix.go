//go:build linux || darwin || freebsd || openbsd || netbsd

package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// remoteShellClient connects to a remoteShellServer, puts the local terminal
// into raw mode, forwards resize events, and provides a full interactive PTY
// session — the "terminal AnyDesk" experience.
func remoteShellClient(ctx context.Context, conn net.Conn) {
	// 1. Read server's OS/shell banner.
	msgType, payload, err := protocol.ReadPacket(conn)
	if err != nil {
		fmt.Fprintln(os.Stderr, cliErr.Render("connect: "+err.Error()))
		return
	}
	if msgType == protocol.MsgSysInfo {
		fmt.Println(clientStyle.Render("  Remote → "+string(payload)) +
			"\n" + cliInfo.Render("  Press Ctrl+] to detach\n"))
	}

	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		// Running piped/non-interactive — plain framed relay without raw mode.
		remoteShellRelay(ctx, conn)
		return
	}

	// 2. Raw mode — all keystrokes go straight to the remote PTY.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, cliErr.Render("raw terminal: "+err.Error()))
		return
	}
	defer func() {
		term.Restore(fd, oldState) //nolint:errcheck
		fmt.Print("\r\n")
	}()

	// 3. Send initial terminal size so remote PTY is the right dimensions.
	if w, h, err := term.GetSize(fd); err == nil {
		protocol.SendPacket(conn, protocol.MsgResize, protocol.EncodeResize(uint16(h), uint16(w))) //nolint:errcheck
	}

	// 4. Forward SIGWINCH (window resize) → MsgResize.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			if w, h, err := term.GetSize(fd); err == nil {
				protocol.SendPacket(conn, protocol.MsgResize, protocol.EncodeResize(uint16(h), uint16(w))) //nolint:errcheck
			}
		}
	}()

	// 5. stdin → MsgData; Ctrl+] (0x1d) detaches.
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

	// 6. MsgData → stdout (main loop; exits when connection closes).
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

// remoteShellRelay is a simple non-raw fallback used when stdin is not a TTY.
func remoteShellRelay(ctx context.Context, conn net.Conn) {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				protocol.SendPacket(conn, protocol.MsgData, buf[:n]) //nolint:errcheck
			}
			if err != nil {
				return
			}
		}
	}()
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
