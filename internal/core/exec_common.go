package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// remoteExecServer is the platform-independent dispatcher for exec mode.
//
// Architecture: a single background reader goroutine owns the connection for
// reading. It routes MsgCmdInterrupt to interruptCh and everything else to
// incoming. This means interrupt messages are processed even while a command
// is blocking inside runSingleCommand — the interrupt watcher cancels the
// command's context, which kills the process group.
func remoteExecServer(conn net.Conn) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}

	protocol.SendPacket(conn, protocol.MsgCmdReady, []byte("ncCmdExe exec server ready\n")) //nolint:errcheck
	protocol.SendPacket(conn, protocol.MsgCwd, []byte(cwd))                                  //nolint:errcheck

	type pkt struct {
		typ     uint8
		payload []byte
	}

	incoming    := make(chan pkt, 8)
	interruptCh := make(chan struct{}, 1)

	// Single reader — routes interrupts separately so they're never queued
	// behind a pending MsgCmdRun while a command is already running.
	go func() {
		defer close(interruptCh)
		defer close(incoming)
		for {
			typ, payload, err := protocol.ReadPacket(conn)
			if err != nil {
				return
			}
			if typ == protocol.MsgCmdInterrupt {
				select {
				case interruptCh <- struct{}{}:
				default:
				}
				continue
			}
			incoming <- pkt{typ, payload}
		}
	}()

	var (
		mu     sync.Mutex
		cancel context.CancelFunc
	)
	setCancel := func(c context.CancelFunc) { mu.Lock(); cancel = c; mu.Unlock() }
	interrupt  := func() {
		mu.Lock()
		if cancel != nil {
			cancel()
			cancel = nil
		}
		mu.Unlock()
	}

	// Interrupt watcher — exits when interruptCh is closed (connection gone).
	go func() {
		for range interruptCh {
			interrupt()
		}
	}()

	for m := range incoming {
		switch m.typ {
		case protocol.MsgCmdRun:
			command := strings.TrimSpace(string(m.payload))
			if command == "" {
				continue
			}

			// Built-in: client requests a file from the server.
			if after, found := strings.CutPrefix(command, "__ncCmdExe_sendfile__ "); found {
				if err := sendFile(conn, strings.TrimSpace(after)); err != nil {
					protocol.SendPacket(conn, protocol.MsgData, []byte("error: "+err.Error()+"\n")) //nolint:errcheck
				}
				protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(0)) //nolint:errcheck
				protocol.SendPacket(conn, protocol.MsgCwd, []byte(cwd))                    //nolint:errcheck
				continue
			}

			ctx, c := context.WithCancel(context.Background())
			setCancel(c)

			newCwd := runSingleCommand(ctx, conn, command, cwd)
			cwd = newCwd

			setCancel(nil)
			c() // release context resources

			protocol.SendPacket(conn, protocol.MsgCwd, []byte(cwd)) //nolint:errcheck

		case protocol.MsgFileHeader:
			name, size, err := protocol.DecodeFileHeader(m.payload)
			if err == nil {
				receiveUpload(conn, name, size)
			}
		}
	}

	interrupt() // kill any command still running when connection drops
}

// parseCD resolves a `cd` command against cwd and returns the target directory.
// Returns ("", false) if the command is not a cd.
func parseCD(command, cwd string) (string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed != "cd" && !strings.HasPrefix(trimmed, "cd ") && !strings.HasPrefix(trimmed, "cd\t") {
		return "", false
	}
	arg := strings.TrimSpace(trimmed[2:])

	if arg == "" || arg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cwd, true
		}
		return home, true
	}

	// ~/... expansion
	if strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Clean(filepath.Join(home, arg[2:])), true
		}
	}

	if filepath.IsAbs(arg) {
		return filepath.Clean(arg), true
	}
	return filepath.Clean(filepath.Join(cwd, arg)), true
}

// envWithCwd returns a sanitised environment with PWD set to cwd.
func envWithCwd(cwd string) []string {
	env := cleanEnv()
	for i, e := range env {
		if strings.HasPrefix(e, "PWD=") {
			env[i] = "PWD=" + cwd
			return env
		}
	}
	return append(env, "PWD="+cwd)
}
