//go:build linux || darwin || freebsd || openbsd || netbsd

package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/creack/pty"
	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// remoteShellServer sends OS/shell info then hands off to a full PTY shell.
// This is the server-side entry point for the "terminal AnyDesk" mode.
func remoteShellServer(conn net.Conn) {
	sh, _ := defaultShell()
	info := fmt.Sprintf("os=%s  shell=%s", runtime.GOOS, sh)
	protocol.SendPacket(conn, protocol.MsgSysInfo, []byte(info)) //nolint:errcheck
	spawnShellPTY(conn)
}

// spawnShellPTY spawns an interactive shell connected to a PTY.
// All I/O flows through the framed protocol so the client can send
// MsgResize events and the server forwards raw PTY bytes as MsgData.
//
// Flow:
//
//	client stdin  → MsgData   → server pty stdin
//	server pty stdout → MsgData → client stdout
//	client window resize → MsgResize → server ptyctl
func spawnShellPTY(conn net.Conn) {
	name, args := defaultShell()
	cmd := exec.Command(name, args...)

	// Set a clean environment: keep PATH/HOME/TERM but strip sensitive vars.
	cmd.Env = cleanEnv()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		// Fall back to plain pipe-based shell if PTY is unavailable.
		fmt.Println(srvWarn.Render(fmt.Sprintf("PTY unavailable (%v), falling back to pipe shell", err)))
		spawnShellPipe(conn)
		return
	}
	defer func() {
		ptmx.Close()
		cmd.Wait() //nolint:errcheck
	}()

	// Handle SIGWINCH forwarded from the client via MsgResize packets.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	// ── PTY → network ─────────────────────────────────────────────────────
	go func() {
		buf := make([]byte, 4*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if e := protocol.SendPacket(conn, protocol.MsgData, buf[:n]); e != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// ── network → PTY / resize ────────────────────────────────────────────
	for {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return
		}
		switch msgType {
		case protocol.MsgData:
			ptmx.Write(payload) //nolint:errcheck
		case protocol.MsgResize:
			rows, cols, err := protocol.DecodeResize(payload)
			if err == nil {
				pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols}) //nolint:errcheck
			}
		case protocol.MsgCmdRun:
			// Single-command execution inside the running shell.
			payload = append(payload, '\n')
			ptmx.Write(payload) //nolint:errcheck
		}
	}
}

// spawnShellPipe is the fallback when PTY is unavailable (e.g. in a container).
func spawnShellPipe(conn net.Conn) {
	name, args := defaultShell()
	cmd := exec.Command(name, args...)
	cmd.Env = cleanEnv()
	cmd.Stdin = conn
	cmd.Stdout = NewFlusher(conn)
	cmd.Stderr = NewFlusher(conn)
	if err := cmd.Run(); err != nil {
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("[shell exited] %v\n", err))) //nolint:errcheck
	}
}

// runSingleCommand executes one shell command, streams output as MsgData frames,
// sends MsgCmdExit, and returns the (possibly updated) working directory.
//
// Cancelling ctx kills the entire process group so no orphans are left behind.
// cd is handled as a built-in: no subprocess, just returns the new cwd.
func runSingleCommand(ctx context.Context, conn net.Conn, command, cwd string) (newCwd string) {
	newCwd = cwd

	// Built-in: cd — no subprocess, just resolve and validate the path.
	if dir, ok := parseCD(command, cwd); ok {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			msg := "cd: " + dir + ": no such directory\n"
			protocol.SendPacket(conn, protocol.MsgData, []byte(msg))   //nolint:errcheck
			protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1)) //nolint:errcheck
			return
		}
		newCwd = dir
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(0)) //nolint:errcheck
		return
	}

	sh, _ := defaultShell()
	cmd := exec.Command(sh, "-c", command)
	cmd.Env = envWithCwd(cwd)
	cmd.Dir = cwd
	// Put the process in its own group so we can kill all children at once.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	pr, pw, err := os.Pipe()
	if err != nil {
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("pipe error: %v\n", err))) //nolint:errcheck
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1))                 //nolint:errcheck
		return
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("start error: %v\n", err))) //nolint:errcheck
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1))                 //nolint:errcheck
		return
	}
	pw.Close()

	// Kill the whole process group when context is cancelled (Ctrl+C or disconnect).
	procDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck
			}
		case <-procDone:
		}
	}()

	buf := make([]byte, 4*1024)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			protocol.SendPacket(conn, protocol.MsgData, buf[:n]) //nolint:errcheck
		}
		if err != nil {
			break
		}
	}
	pr.Close()
	close(procDone)

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			// Killed by interrupt — signal the client with the conventional code.
			protocol.SendPacket(conn, protocol.MsgData, []byte("\n"))
			protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(130)) //nolint:errcheck
			return
		}
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(exitCode)) //nolint:errcheck
	return
}

// cleanEnv returns a safe environment for spawned commands.
func cleanEnv() []string {
	keep := []string{"PATH", "HOME", "USER", "SHELL", "TERM", "LANG", "LC_ALL"}
	env := make([]string, 0, len(keep))
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if os.Getenv("TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	return env
}


// sendFile transfers a local file to the remote via the framed protocol.
func sendFile(conn net.Conn, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}

	// Send header.
	hdr := protocol.EncodeFileHeader(info.Name(), uint64(info.Size()))
	if err := protocol.SendPacket(conn, protocol.MsgFileHeader, hdr); err != nil {
		return err
	}

	// Stream chunks.
	buf := make([]byte, 32*1024)
	sent := int64(0)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if e := protocol.SendPacket(conn, protocol.MsgFileData, buf[:n]); e != nil {
				return e
			}
			sent += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}

	// Signal completion.
	return protocol.SendPacket(conn, protocol.MsgFileDone, nil)
}

// recvFile receives a file sent by sendFile and writes it to destDir.
func recvFile(conn net.Conn, destDir string) (string, error) {
	// First packet must be MsgFileHeader.
	msgType, payload, err := protocol.ReadPacket(conn)
	if err != nil {
		return "", err
	}
	if msgType != protocol.MsgFileHeader {
		return "", fmt.Errorf("expected file header, got type %d", msgType)
	}
	name, size, err := protocol.DecodeFileHeader(payload)
	if err != nil {
		return "", err
	}

	destPath := fmt.Sprintf("%s/%s", destDir, name)
	out, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", destPath, err)
	}
	defer out.Close()

	received := uint64(0)
	for received < size {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return destPath, err
		}
		switch msgType {
		case protocol.MsgFileData:
			if _, err := out.Write(payload); err != nil {
				return destPath, err
			}
			received += uint64(len(payload))
		case protocol.MsgFileDone:
			goto done
		case protocol.MsgFileError:
			return destPath, fmt.Errorf("remote error: %s", string(payload))
		}
	}
done:
	protocol.SendPacket(conn, protocol.MsgFileAck, nil) //nolint:errcheck
	return destPath, nil
}

// receiveUpload handles an incoming file from the client (already past the header).
func receiveUpload(conn net.Conn, name string, size uint64) {
	out, err := os.Create(name)
	if err != nil {
		protocol.SendPacket(conn, protocol.MsgFileError, []byte(err.Error())) //nolint:errcheck
		return
	}
	defer out.Close()

	received := uint64(0)
	for received < size {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return
		}
		switch msgType {
		case protocol.MsgFileData:
			out.Write(payload) //nolint:errcheck
			received += uint64(len(payload))
		case protocol.MsgFileDone:
			goto done
		}
	}
done:
	protocol.SendPacket(conn, protocol.MsgFileAck, nil) //nolint:errcheck
}
