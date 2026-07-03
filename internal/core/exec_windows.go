//go:build windows

package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"

	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// remoteShellServer sends OS/shell info then serves a framed pipe shell.
func remoteShellServer(conn net.Conn) {
	sh, _ := defaultShell()
	info := fmt.Sprintf("os=%s  shell=%s", runtime.GOOS, sh)
	protocol.SendPacket(conn, protocol.MsgSysInfo, []byte(info)) //nolint:errcheck
	framedShellPipe(conn)
}

// framedShellPipe runs a shell with stdin/stdout over the framed MsgData protocol.
// Used on Windows where no PTY is available.
func framedShellPipe(conn net.Conn) {
	name, args := defaultShell()
	cmd := exec.Command(name, args...)
	cmd.Env = cleanEnv()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("pipe error: %v\r\n", err))) //nolint:errcheck
		return
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("pipe error: %v\r\n", err))) //nolint:errcheck
		return
	}

	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW

	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("start error: %v\r\n", err))) //nolint:errcheck
		return
	}
	stdinR.Close()
	stdoutW.Close()

	done := make(chan struct{})

	// network MsgData → shell stdin
	go func() {
		defer stdinW.Close()
		for {
			msgType, payload, err := protocol.ReadPacket(conn)
			if err != nil {
				return
			}
			if msgType == protocol.MsgData {
				stdinW.Write(payload) //nolint:errcheck
			}
		}
	}()

	// shell stdout → network MsgData
	go func() {
		defer close(done)
		buf := make([]byte, 4*1024)
		for {
			n, err := stdoutR.Read(buf)
			if n > 0 {
				protocol.SendPacket(conn, protocol.MsgData, buf[:n]) //nolint:errcheck
			}
			if err != nil {
				break
			}
		}
	}()

	<-done
	cmd.Wait() //nolint:errcheck
}

// spawnShellPTY falls back to pipe-based shell on Windows (no PTY support).
func spawnShellPTY(conn net.Conn) {
	spawnShellPipe(conn)
}

func spawnShellPipe(conn net.Conn) {
	name, args := defaultShell()
	cmd := exec.Command(name, args...)
	cmd.Env = cleanEnv()
	cmd.Stdin = conn
	cmd.Stdout = NewFlusher(conn)
	cmd.Stderr = NewFlusher(conn)
	if err := cmd.Run(); err != nil {
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("[shell exited] %v\r\n", err))) //nolint:errcheck
	}
}

// runSingleCommand executes one command, streams output, and returns the
// (possibly updated) working directory. cd is handled as a built-in.
// Cancelling ctx kills the process so no orphans remain on disconnect.
func runSingleCommand(ctx context.Context, conn net.Conn, command, cwd string) (newCwd string) {
	newCwd = cwd

	if dir, ok := parseCD(command, cwd); ok {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			protocol.SendPacket(conn, protocol.MsgData, []byte("cd: "+dir+": no such directory\r\n")) //nolint:errcheck
			protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1))                //nolint:errcheck
			return
		}
		newCwd = dir
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(0)) //nolint:errcheck
		return
	}

	sh, shArgs := defaultShell()
	args := append(shArgs, "/C", command)
	cmd := exec.Command(sh, args...)
	cmd.Env = envWithCwd(cwd)
	cmd.Dir = cwd

	pr, pw, err := os.Pipe()
	if err != nil {
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("pipe error: %v\r\n", err))) //nolint:errcheck
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1))                   //nolint:errcheck
		return
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		protocol.SendPacket(conn, protocol.MsgData, []byte(fmt.Sprintf("start error: %v\r\n", err))) //nolint:errcheck
		protocol.SendPacket(conn, protocol.MsgCmdExit, protocol.EncodeExitCode(1))                    //nolint:errcheck
		return
	}
	pw.Close()

	procDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				cmd.Process.Kill() //nolint:errcheck
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
			protocol.SendPacket(conn, protocol.MsgData, []byte("\r\n"))
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

func cleanEnv() []string {
	keep := []string{"PATH", "USERPROFILE", "USERNAME", "COMSPEC", "SystemRoot", "TEMP", "TMP"}
	env := make([]string, 0, len(keep))
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}


func sendFile(conn net.Conn, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	hdr := protocol.EncodeFileHeader(info.Name(), uint64(info.Size()))
	if err := protocol.SendPacket(conn, protocol.MsgFileHeader, hdr); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if e := protocol.SendPacket(conn, protocol.MsgFileData, buf[:n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return protocol.SendPacket(conn, protocol.MsgFileDone, nil)
}

func recvFile(conn net.Conn, destDir string) (string, error) {
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
	destPath := destDir + "\\" + name
	out, err := os.Create(destPath)
	if err != nil {
		return "", err
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
			out.Write(payload) //nolint:errcheck
			received += uint64(len(payload))
		case protocol.MsgFileDone:
			goto done
		case protocol.MsgFileError:
			return destPath, fmt.Errorf("remote: %s", string(payload))
		}
	}
done:
	protocol.SendPacket(conn, protocol.MsgFileAck, nil) //nolint:errcheck
	return destPath, nil
}

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
