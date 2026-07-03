package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/lipgloss"
	"github.com/prem0x01/ncCmdExe/internal/protocol"
)

// shortPath replaces the home directory prefix with ~.
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") || strings.HasPrefix(p, home+"\\") {
		return "~" + p[len(home):]
	}
	return p
}

// ── styles ────────────────────────────────────────────────────────────────────

var (
	clientStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00BFFF")).Bold(true)
	cliInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB"))
	cliErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5f56")).Bold(true)
	cliPrompt   = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575")).Bold(true)
	cliExit     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500"))
)

// ── Client ────────────────────────────────────────────────────────────────────

// Client holds connection parameters.
type Client struct {
	host        string
	port        int
	udp         bool
	timeout     int
	keepAlive   bool
	streaming   bool   // receive MJPEG screen stream
	execMode    bool   // framed exec REPL
	remoteShell bool   // full PTY interactive shell (--tty)
	sendFile    string // local path to upload (empty = off)
	recvFile    bool   // receive a file from server
}

// NewClient creates a configured Client.
func NewClient(host string, port int, udp bool, timeout int, keepAlive bool, streaming bool, execMode bool) *Client {
	return &Client{
		host:      host,
		port:      port,
		udp:       udp,
		timeout:   timeout,
		keepAlive: keepAlive,
		streaming: streaming,
		execMode:  execMode,
	}
}

// WithFileSend sets the local file to upload after connecting.
func (c *Client) WithFileSend(path string) *Client  { c.sendFile = path; return c }

// WithFileRecv tells the client to receive a file from the server.
func (c *Client) WithFileRecv(recv bool) *Client { c.recvFile = recv; return c }

// WithRemoteShell enables the full PTY interactive shell mode.
func (c *Client) WithRemoteShell(v bool) *Client { c.remoteShell = v; return c }

// TestConnection dials and immediately closes.
func (c *Client) TestConnection() error {
	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := net.DialTimeout("tcp", addr, time.Duration(c.timeout)*time.Second)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// Connect establishes the connection and blocks until closed or Ctrl+C.
func (c *Client) Connect() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	proto := "tcp"
	if c.udp {
		proto = "udp"
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	dialer := &net.Dialer{Timeout: time.Duration(c.timeout) * time.Second}
	conn, err := dialer.DialContext(ctx, proto, addr)
	if err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("✗  %s: %v", addr, err)))
		return
	}
	defer conn.Close()

	if c.keepAlive {
		if tcp, ok := conn.(*net.TCPConn); ok {
			tcp.SetKeepAlive(true)
			tcp.SetKeepAlivePeriod(30 * time.Second)
		}
	}
	setNoDelay(conn)

	fmt.Println(clientStyle.Render(fmt.Sprintf("✔  Connected to %s://%s", proto, addr)))

	go func() { <-ctx.Done(); conn.Close() }()

	switch {
	case c.remoteShell:
		remoteShellClient(ctx, conn)
	case c.streaming:
		c.receiveStream(ctx, conn)
	case c.execMode:
		c.execREPL(ctx, conn)
	case c.sendFile != "":
		c.uploadFile(conn)
	case c.recvFile:
		c.downloadFile(conn)
	default:
		c.relay(ctx, conn)
	}
}

// ── relay ─────────────────────────────────────────────────────────────────────

func (c *Client) relay(ctx context.Context, conn net.Conn) {
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

// ── exec REPL ─────────────────────────────────────────────────────────────────

// execREPL is the interactive command execution client.
//
// The prompt shows the remote working directory. Ctrl+C sends MsgCmdInterrupt
// to kill the running command without closing the session. cd is handled
// server-side and the new cwd is returned via MsgCwd after every command.
//
// Client-side built-ins:
//
//	!upload <local>    upload a file to the server
//	!download <remote> request a file from the server
//	!help              show this list
//	!exit / exit       close the session
func (c *Client) execREPL(ctx context.Context, conn net.Conn) {
	// Wait for MsgCmdReady.
	msgType, payload, err := protocol.ReadPacket(conn)
	if err != nil || msgType != protocol.MsgCmdReady {
		fmt.Println(cliErr.Render("Server did not send ready signal."))
		return
	}
	fmt.Print(cliInfo.Render(string(payload)))

	// Read initial working directory sent right after MsgCmdReady.
	cwd := ""
	if typ2, p2, err2 := protocol.ReadPacket(conn); err2 == nil && typ2 == protocol.MsgCwd {
		cwd = string(p2)
	}

	printHelp := func() {
		fmt.Println(cliInfo.Render(`
  Built-in client commands:
    !upload <path>   — upload a local file to the server
    !download <name> — download a file from the server
    !help            — show this message
    !exit            — end the session
  Ctrl+C             — interrupt the running command (does not close session)
  Any other input is sent to the server as a shell command.
`))
	}

	stdin := bufio.NewReader(os.Stdin)
	for {
		prompt := shortPath(cwd) + " $ "
		fmt.Print(cliPrompt.Render(prompt))

		line, err := stdin.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		// Client-side built-ins.
		if strings.HasPrefix(line, "!") || line == "exit" || line == "quit" {
			parts := strings.Fields(line)
			switch parts[0] {
			case "!exit", "!quit", "exit", "quit":
				fmt.Println(cliInfo.Render("Session closed."))
				return
			case "!help":
				printHelp()
				continue
			case "!upload":
				if len(parts) < 2 {
					fmt.Println(cliErr.Render("Usage: !upload <local-path>"))
					continue
				}
				c.execUpload(conn, parts[1])
				continue
			case "!download":
				if len(parts) < 2 {
					fmt.Println(cliErr.Render("Usage: !download <remote-file>"))
					continue
				}
				c.execDownload(conn, parts[1])
				continue
			default:
				fmt.Println(cliErr.Render(fmt.Sprintf("Unknown built-in: %s  (type !help for list)", parts[0])))
				continue
			}
		}

		if err := protocol.SendPacket(conn, protocol.MsgCmdRun, []byte(line)); err != nil {
			fmt.Println(cliErr.Render(fmt.Sprintf("send: %v", err)))
			return
		}

		exitCode := c.collectWithInterrupt(ctx, conn, &cwd)
		if exitCode == 130 {
			fmt.Println(cliExit.Render("^C"))
		} else if exitCode != 0 {
			fmt.Println(cliExit.Render(fmt.Sprintf("[exit %d]", exitCode)))
		}

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// collectWithInterrupt reads output frames until MsgCmdExit.
// Ctrl+C (SIGINT) sends MsgCmdInterrupt to the server rather than killing
// the client. cwdOut is updated whenever a MsgCwd frame arrives.
func (c *Client) collectWithInterrupt(ctx context.Context, conn net.Conn, cwdOut *string) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	defer close(done)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			protocol.SendPacket(conn, protocol.MsgCmdInterrupt, nil) //nolint:errcheck
		case <-done:
		case <-ctx.Done():
		}
	}()

	for {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return -1
		}
		switch msgType {
		case protocol.MsgData:
			os.Stdout.Write(payload) //nolint:errcheck
		case protocol.MsgCmdExit:
			return protocol.DecodeExitCode(payload)
		case protocol.MsgCwd:
			if cwdOut != nil {
				*cwdOut = string(payload)
			}
		case protocol.MsgFileHeader:
			fmt.Println(cliInfo.Render("← receiving file…"))
			name, size, err := protocol.DecodeFileHeader(payload)
			if err != nil {
				fmt.Println(cliErr.Render(fmt.Sprintf("file header: %v", err)))
				return -1
			}
			c.receiveFileInline(conn, name, size)
		}
	}
}

// collectOutput is the simple variant used by file download flows (no interrupt, no cwd).
func (c *Client) collectOutput(conn net.Conn) int {
	return c.collectWithInterrupt(context.Background(), conn, nil)
}

// execUpload sends a file to the server during a REPL session.
// It sends a special MsgCmdRun marker first so the server's exec loop
// knows to expect file transfer frames next.
func (c *Client) execUpload(conn net.Conn, localPath string) {
	info, err := os.Stat(localPath)
	if err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("stat: %v", err)))
		return
	}
	fmt.Println(cliInfo.Render(fmt.Sprintf("Uploading %s (%d bytes)…", info.Name(), info.Size())))

	if err := sendFile(conn, localPath); err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("upload: %v", err)))
		return
	}

	// Wait for server ack.
	msgType, _, err := protocol.ReadPacket(conn)
	if err != nil || msgType != protocol.MsgFileAck {
		fmt.Println(cliErr.Render("no ack from server"))
		return
	}
	fmt.Println(clientStyle.Render(fmt.Sprintf("✔  Uploaded %s", info.Name())))
}

// execDownload asks the server to send a named file.
func (c *Client) execDownload(conn net.Conn, remoteName string) {
	// Tell the server we want a file by sending a special command.
	cmd := "__ncCmdExe_sendfile__ " + remoteName
	if err := protocol.SendPacket(conn, protocol.MsgCmdRun, []byte(cmd)); err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("send: %v", err)))
		return
	}
	// Collect response — may be MsgData (error text) then MsgCmdExit,
	// or a file transfer sequence.
	c.collectOutput(conn)
}

// receiveFileInline saves a file transfer that arrives mid-REPL.
func (c *Client) receiveFileInline(conn net.Conn, name string, size uint64) {
	dest := filepath.Join(".", name)
	out, err := os.Create(dest)
	if err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("create %s: %v", dest, err)))
		return
	}
	defer out.Close()

	received := uint64(0)
	for received < size {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			fmt.Println(cliErr.Render(fmt.Sprintf("recv: %v", err)))
			return
		}
		switch msgType {
		case protocol.MsgFileData:
			out.Write(payload) //nolint:errcheck
			received += uint64(len(payload))
		case protocol.MsgFileDone:
			goto done
		case protocol.MsgFileError:
			fmt.Println(cliErr.Render(fmt.Sprintf("remote error: %s", string(payload))))
			return
		}
	}
done:
	protocol.SendPacket(conn, protocol.MsgFileAck, nil) //nolint:errcheck
	fmt.Println(clientStyle.Render(fmt.Sprintf("✔  Saved %s (%d bytes)", dest, received)))
}

// ── file transfer helpers (non-exec mode) ────────────────────────────────────

func (c *Client) uploadFile(conn net.Conn) {
	info, err := os.Stat(c.sendFile)
	if err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("stat: %v", err)))
		return
	}
	fmt.Println(cliInfo.Render(fmt.Sprintf("Uploading %s (%d bytes)…", info.Name(), info.Size())))
	if err := sendFile(conn, c.sendFile); err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("upload failed: %v", err)))
		return
	}
	// Wait for ack.
	msgType, _, err := protocol.ReadPacket(conn)
	if err == nil && msgType == protocol.MsgFileAck {
		fmt.Println(clientStyle.Render("✔  Upload complete"))
	}
}

func (c *Client) downloadFile(conn net.Conn) {
	dest, err := recvFile(conn, ".")
	if err != nil {
		fmt.Println(cliErr.Render(fmt.Sprintf("download failed: %v", err)))
		return
	}
	fmt.Println(clientStyle.Render(fmt.Sprintf("✔  Saved to %s", dest)))
}

// ── screen stream receiver ────────────────────────────────────────────────────

type atomicFrame struct{ p atomic.Pointer[[]byte] }

func (af *atomicFrame) store(b []byte) { af.p.Store(&b) }
func (af *atomicFrame) load() []byte {
	if p := af.p.Load(); p != nil {
		return *p
	}
	return nil
}

type subscriber chan struct{}

type broadcaster struct{ subs atomic.Pointer[[]subscriber] }

func newBroadcaster() *broadcaster {
	b := &broadcaster{}
	empty := []subscriber{}
	b.subs.Store(&empty)
	return b
}

func (b *broadcaster) subscribe() subscriber {
	ch := make(subscriber, 1)
	for {
		old := b.subs.Load()
		next := make([]subscriber, len(*old)+1)
		copy(next, *old)
		next[len(*old)] = ch
		if b.subs.CompareAndSwap(old, &next) {
			return ch
		}
	}
}

func (b *broadcaster) unsubscribe(ch subscriber) {
	for {
		old := b.subs.Load()
		next := make([]subscriber, 0, len(*old))
		for _, s := range *old {
			if s != ch {
				next = append(next, s)
			}
		}
		if b.subs.CompareAndSwap(old, &next) {
			return
		}
	}
}

func (b *broadcaster) notify() {
	for _, s := range *b.subs.Load() {
		select {
		case s <- struct{}{}:
		default:
		}
	}
}

const (
	mjpegBoundary = "mjpegframe"
	viewerPort    = 7070
)

func (c *Client) receiveStream(ctx context.Context, conn net.Conn) {
	frame := &atomicFrame{}
	bc := newBroadcaster()
	viewerAddr := fmt.Sprintf("http://localhost:%d", viewerPort)
	done := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, viewerHTML, c.host, c.host)
	})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", mjpegBoundary))
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		sub := bc.subscribe()
		defer bc.unsubscribe(sub)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case <-done:
				return
			case <-sub:
			}
			f := frame.load()
			if f == nil {
				continue
			}
			fmt.Fprintf(w, "--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
				mjpegBoundary, len(f))
			if _, err := w.Write(f); err != nil {
				return
			}
			fmt.Fprintf(w, "\r\n")
			flusher.Flush()
		}
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		f := frame.load()
		size := 0
		if f != nil {
			size = len(f)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"connected":true,"frame_bytes":%d}`, size)
	})

	srv := &http.Server{
		Addr:        fmt.Sprintf(":%d", viewerPort),
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Println(cliErr.Render(fmt.Sprintf("viewer: %v", err)))
		}
	}()
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()

	fmt.Println(clientStyle.Render("✔  Stream receiver ready"))
	fmt.Println(cliInfo.Render(fmt.Sprintf("   Viewer → %s", viewerAddr)))
	fmt.Println(cliInfo.Render("   Ctrl+C to stop."))

	time.Sleep(150 * time.Millisecond)
	openBrowser(viewerAddr)

	for {
		msgType, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			fmt.Println(cliInfo.Render("Stream ended."))
			close(done)
			return
		}
		if msgType == protocol.MsgFrame {
			frame.store(payload)
			bc.notify()
		}
	}
}

// ── browser launcher ──────────────────────────────────────────────────────────

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		for _, launcher := range []string{"xdg-open", "gnome-open", "kde-open", "sensible-browser"} {
			if _, err := exec.LookPath(launcher); err == nil {
				cmd, args = launcher, []string{url}
				break
			}
		}
		if cmd == "" {
			fmt.Println(cliInfo.Render(fmt.Sprintf("Open your browser: %s", url)))
			return
		}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		fmt.Println(cliInfo.Render(fmt.Sprintf("Open your browser: %s", url)))
	}
}

// ── viewer HTML ───────────────────────────────────────────────────────────────

const viewerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>ncCmdExe · %s</title>
<style>
  *{margin:0;padding:0;box-sizing:border-box}
  body{background:#0d0d0d;display:flex;flex-direction:column;
       align-items:center;justify-content:center;min-height:100vh;font-family:monospace;color:#ccc}
  #bar{width:100%%;padding:6px 16px;background:#111;display:flex;
       align-items:center;gap:12px;border-bottom:1px solid #222}
  #bar span{font-size:.8rem}
  #dot{width:8px;height:8px;border-radius:50%%;background:#555}
  #dot.ok{background:#04B575}
  #frame{max-width:100%%;max-height:calc(100vh - 34px);display:block}
</style>
</head>
<body>
<div id="bar">
  <div id="dot"></div>
  <span id="label">connecting…</span>
  <span style="margin-left:auto;color:#555">ncCmdExe · %s</span>
</div>
<img id="frame" alt="stream">
<script>
(function(){
  var img=document.getElementById('frame');
  var dot=document.getElementById('dot');
  var lbl=document.getElementById('label');
  var fps=0,t=Date.now();
  function connect(){
    img.onload=function(){dot.className='ok';fps++;var n=Date.now();if(n-t>=1000){lbl.textContent='live · '+fps+' fps';fps=0;t=n;}};
    img.onerror=function(){dot.className='';lbl.textContent='reconnecting…';setTimeout(connect,1500);};
    img.src='/stream?t='+Date.now();
  }
  connect();
})();
</script>
</body>
</html>`

var _ = unsafe.Pointer(nil)
