// Package core contains the listener (Server) and dialer (Client) logic.
package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/prem0x01/ncCmdExe/internal/protocol"
	"github.com/prem0x01/ncCmdExe/internal/stream"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	serverStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Bold(true)
	srvInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB"))
	srvWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Bold(true)
	srvErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5f56")).Bold(true)
)

// ── Server ────────────────────────────────────────────────────────────────────

// Server holds all configuration for a listener.
type Server struct {
	port        int
	udp         bool
	execute     string // single command to run on connect (-e)
	shell       bool   // interactive bind shell (-s)
	keepAlive   bool
	streaming   bool // screen stream (--stream)
	execMode    bool // framed exec REPL (--exec-mode)
	remoteShell bool // full PTY remote-shell with OS handshake (--tty)
}

// NewServer creates a configured Server.
func NewServer(port int, udp bool, execute string, shell bool, keepAlive bool, streaming bool, execMode bool) *Server {
	return &Server{
		port:      port,
		udp:       udp,
		execute:   execute,
		shell:     shell,
		keepAlive: keepAlive,
		streaming: streaming,
		execMode:  execMode,
	}
}

// WithRemoteShell enables the full PTY remote-shell mode.
func (s *Server) WithRemoteShell(v bool) *Server { s.remoteShell = v; return s }

// Start begins listening and blocks until SIGINT/SIGTERM.
func (s *Server) Start() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf(":%d", s.port)
	if s.udp {
		s.startUDP(ctx, addr)
		return
	}

	lc := net.ListenConfig{Control: setReusePort}
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	defer listener.Close()

	fmt.Println(serverStyle.Render(fmt.Sprintf("Listening on tcp://%s  [%s]", addr, s.activeMode())))
	fmt.Println(srvInfo.Render("Waiting for connections… Ctrl+C to stop."))

	go func() { <-ctx.Done(); listener.Close() }()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				fmt.Println(srvInfo.Render("Shutting down."))
				return
			default:
				log.Printf("accept: %v", err)
				continue
			}
		}
		fmt.Println(serverStyle.Render(fmt.Sprintf("↳ connection from %s", conn.RemoteAddr())))
		go s.handleConnection(ctx, conn)
	}
}

// activeMode returns a short human-readable mode label.
func (s *Server) activeMode() string {
	switch {
	case s.remoteShell:
		return "remote-shell (PTY)"
	case s.streaming:
		return "screen-stream"
	case s.execMode:
		return "exec-repl"
	case s.shell:
		return "bind-shell (PTY)"
	case s.execute != "":
		return fmt.Sprintf("exec: %s", s.execute)
	default:
		return "relay"
	}
}

// ── UDP listener ──────────────────────────────────────────────────────────────

func (s *Server) startUDP(ctx context.Context, addr string) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatalf("udp listen %s: %v", addr, err)
	}
	defer pc.Close()

	fmt.Println(serverStyle.Render(fmt.Sprintf("Listening on udp://%s", addr)))
	go func() { <-ctx.Done(); pc.Close() }()

	buf := make([]byte, 64*1024)
	for {
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("udp read: %v", err)
				continue
			}
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		if s.execute != "" {
			out := runCommand(s.execute)
			pc.WriteTo(out, raddr) //nolint:errcheck
		} else {
			fmt.Printf("[%s] %s", raddr, string(data))
		}
	}
}

// ── TCP connection dispatcher ─────────────────────────────────────────────────

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if s.keepAlive {
		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetKeepAlive(true)
			tcp.SetKeepAlivePeriod(30 * time.Second)
		}
	}
	setNoDelay(conn)

	switch {
	case s.remoteShell:
		remoteShellServer(conn) // full PTY + OS handshake
	case s.streaming:
		s.streamScreen(ctx, conn)
	case s.execMode:
		remoteExecServer(conn) // framed exec REPL
	case s.shell:
		spawnShellPTY(conn) // PTY-backed interactive shell
	case s.execute != "":
		s.executeCommand(conn, s.execute) // single fixed command
	default:
		s.relay(ctx, conn) // plain relay
	}
}

// ── screen streaming ──────────────────────────────────────────────────────────

// streamScreen pipelines capture → send with a 1-deep channel so the sender
// always ships the freshest frame and the encoder is never network-blocked.
func (s *Server) streamScreen(ctx context.Context, conn net.Conn) {
	fmt.Println(srvInfo.Render("Screen streaming → sending frames…"))

	frameCh := make(chan []byte, 1)

	go func() {
		errCount := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			frame, err := stream.CaptureJPEG()
			if err != nil {
				errCount++
				sleep := time.Duration(float64(50*time.Millisecond) * math.Pow(2, float64(errCount-1)))
				if sleep > 2*time.Second {
					sleep = 2 * time.Second
				}
				if errCount == 1 {
					fmt.Println(srvWarn.Render(fmt.Sprintf("capture: %v", err)))
				}
				time.Sleep(sleep)
				continue
			}
			errCount = 0

			select {
			case frameCh <- frame:
			default:
				select {
				case <-frameCh:
				default:
				}
				frameCh <- frame
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-frameCh:
			if err := protocol.SendPacket(conn, protocol.MsgFrame, frame); err != nil {
				fmt.Println(srvInfo.Render("Stream client disconnected."))
				return
			}
		}
	}
}

// ── single-command execution ──────────────────────────────────────────────────

// executeCommand runs one fixed command, wiring the connection as its I/O.
// This is the classic netcat -e behaviour; no framing is used.
func (s *Server) executeCommand(conn net.Conn, command string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return
	}
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = cleanEnv()
	cmd.Stdin = conn
	cmd.Stdout = NewFlusher(conn)
	cmd.Stderr = NewFlusher(conn)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(conn, "[error] %v\n", err)
	}
}

// ── relay ─────────────────────────────────────────────────────────────────────

func (s *Server) relay(ctx context.Context, conn net.Conn) {
	const bufSize = 64 * 1024
	done := make(chan struct{})
	go func() {
		buf := make([]byte, bufSize)
		io.CopyBuffer(conn, os.Stdin, buf) //nolint:errcheck
		close(done)
	}()
	buf := make([]byte, bufSize)
	io.CopyBuffer(os.Stdout, conn, buf) //nolint:errcheck
	<-done
}

// ── helpers ───────────────────────────────────────────────────────────────────

func runCommand(command string) []byte {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}
	out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
	if err != nil {
		out = append(out, fmt.Sprintf("\n[error] %v\n", err)...)
	}
	return out
}

func defaultShell() (string, []string) {
	if runtime.GOOS == "windows" {
		if sh := os.Getenv("COMSPEC"); sh != "" {
			return sh, nil
		}
		return "cmd.exe", nil
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh, []string{"-i"}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return "bash", []string{"-i"}
	}
	return "/bin/sh", []string{"-i"}
}

func setNoDelay(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true) //nolint:errcheck
	}
}

// ── Flusher ───────────────────────────────────────────────────────────────────

type Flusher struct{ w *bufio.Writer }

func NewFlusher(w io.Writer) *Flusher {
	return &Flusher{w: bufio.NewWriterSize(w, 4*1024)}
}

func (f *Flusher) Write(b []byte) (int, error) {
	n, err := f.w.Write(b)
	if err != nil {
		return n, err
	}
	return n, f.w.Flush()
}
