package cmd

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/prem0x01/ncCmdExe/internal/core"
	"github.com/prem0x01/ncCmdExe/internal/scanner"
	"github.com/prem0x01/ncCmdExe/internal/ui"
	"github.com/spf13/cobra"
)

// ── flags ─────────────────────────────────────────────────────────────────────

var (
	listen     bool
	port       int
	host       string
	udp        bool
	execute    string
	shell      bool
	scan       bool
	scanPorts  string
	scanRange  string
	version    bool
	verbose    bool
	timeout    int
	keepAlive  bool
	streaming  bool
	execMode   bool   // framed exec REPL  (server: --exec-mode  client: --exec-mode)
	ttyMode    bool   // full PTY remote shell (server: serve PTY  client: attach with raw terminal)
	uploadFile string // client: upload this file after connecting
	recvFile   bool   // client: receive a file from server
)

// ── root command ──────────────────────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "ncCmdExe [host]",
	Short: "Netcat-style toolkit written in Go",
	Long: `
                      /$$$$$$                      /$$ /$$$$$$$$
                     /$$__  $$                    | $$| $$_____/
 /$$$$$$$   /$$$$$$$| $$  \__/ /$$$$$$/$$$$   /$$$$$$$| $$       /$$   /$$  /$$$$$$
| $$__  $$ /$$_____/| $$      | $$_  $$_  $$ /$$__  $$| $$$$$   |  $$ /$$/ /$$__  $$
| $$  \ $$| $$      | $$      | $$ \ $$ \ $$| $$  | $$| $$__/    \  $$$$/ | $$$$$$$$
| $$  | $$| $$      | $$    $$| $$ | $$ | $$| $$  | $$| $$        >$$  $$ | $$_____/
| $$  | $$|  $$$$$$$|  $$$$$$/| $$ | $$ | $$|  $$$$$$$| $$$$$$$$ /$$/\  $$|  $$$$$$$
|__/  |__/ \_______/ \______/ |__/ |__/ |__/ \_______/|________/|__/  \__/ \_______/

  Developed by: prem0x01

  ── Modes ────────────────────────────────────────────────────────────────────

  (no flags)                  Open the interactive TUI menu
  -l                          Listen — plain relay
  -l -s                       Listen — PTY bind shell (arrow keys, tab, etc.)
  -l -e "cmd"                 Listen — run fixed command on connect (classic -e)
  -l --exec-mode              Listen — interactive exec REPL with file transfer
  -l --stream                 Listen — stream screen to connecting client
  <host>                      Connect — relay mode
  <host> --exec-mode          Connect — interactive exec REPL
  <host> --exec-mode --upload <file>   Upload a file then drop to REPL
  <host> --exec-mode --recv   Receive a file from server
  <host> --stream             Connect — view screen stream in browser
  -S <host>                   Scan ports

  ── Examples ─────────────────────────────────────────────────────────────────

  ncCmdExe                                  # TUI menu
  ncCmdExe -l -p 4444 -s                   # PTY bind shell
  ncCmdExe -l -p 4444 --exec-mode          # exec REPL server
  ncCmdExe 10.0.0.5 -p 4444 --exec-mode    # exec REPL client (interactive)
  ncCmdExe 10.0.0.5 -p 4444 --exec-mode --upload secret.txt
  ncCmdExe -l -p 4444 --stream             # stream screen
  ncCmdExe 10.0.0.5 -p 4444 --stream       # watch stream
  ncCmdExe -S 192.168.1.1 --ports 1-1024   # port scan
`,

	RunE: func(cmd *cobra.Command, args []string) error {
		noAction := !listen && !scan && execute == "" && !streaming && !execMode && !ttyMode &&
			uploadFile == "" && !recvFile

		if noAction && len(args) == 0 {
			runTUI("")
			return nil
		}
		if noAction && len(args) == 1 {
			runTUI(args[0])
			return nil
		}
		return handleActions(args)
	},
}

func init() {
	f := rootCmd.Flags()

	// Connection
	f.BoolVarP(&listen, "listen", "l", false, "Listen for incoming connections")
	f.IntVarP(&port, "port", "p", 4444, "Port number")
	f.StringVarP(&host, "host", "H", "", "Host address (alternative to positional arg)")
	f.BoolVarP(&udp, "udp", "u", false, "Use UDP instead of TCP")
	f.IntVarP(&timeout, "timeout", "t", 5, "Connection / scan timeout in seconds")
	f.BoolVarP(&keepAlive, "keep-alive", "k", false, "Enable TCP keep-alive")

	// Execution modes
	f.StringVarP(&execute, "execute", "e", "", "Execute a fixed command on connect (classic nc -e)")
	f.BoolVarP(&shell, "shell", "s", false, "Spawn PTY interactive shell on connect")
	f.BoolVar(&execMode, "exec-mode", false, "Framed exec REPL with file transfer support")

	// File transfer
	f.StringVar(&uploadFile, "upload", "", "Upload this local file after connecting (requires --exec-mode)")
	f.BoolVar(&recvFile, "recv", false, "Receive a file from the server (requires --exec-mode)")

	// Screen streaming
	f.BoolVar(&streaming, "stream", false, "Stream screen (server) / view stream (client)")

	// Remote shell
	f.BoolVar(&ttyMode, "tty", false, "Remote shell: serve PTY (-l) or attach with raw terminal (client)")

	// Scanning
	f.BoolVarP(&scan, "scan", "S", false, "Enable port scanning")
	f.StringVar(&scanPorts, "ports", "1-1000", "Ports to scan (e.g. 80,443 or 1-1000)")
	f.StringVar(&scanRange, "range", "", "IP range to scan (e.g. 192.168.1.1-192.168.1.254)")
	f.BoolVarP(&version, "version-scan", "v", false, "Service version detection during scan")
	f.BoolVar(&verbose, "verbose", false, "Verbose scan output")
}

// Execute is the entry point called from main.
func Execute() error {
	return rootCmd.Execute()
}

// ── TUI launcher ─────────────────────────────────────────────────────────────

func runTUI(prefillConnect string) {
	m := ui.NewModel()
	if prefillConnect != "" {
		m.StateToConnect(prefillConnect)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}

	act := final.(ui.Model).Action()
	switch act.Kind {
	case "remote-shell-server":
		core.NewServer(act.Port, false, "", false, false, false, false).WithRemoteShell(true).Start()
	case "remote-shell-client":
		fmt.Printf("Attaching to remote shell on %s:%d …\n", act.Host, act.Port)
		core.NewClient(act.Host, act.Port, false, 5, false, false, false).WithRemoteShell(true).Connect()
	case "connect":
		fmt.Printf("Connecting to %s:%d …\n", act.Host, act.Port)
		core.NewClient(act.Host, act.Port, false, 5, false, false, false).Connect()
	case "listen":
		core.NewServer(act.Port, false, "", false, false, false, false).Start()
	case "shell":
		core.NewServer(act.Port, false, "", true, false, false, false).Start()
	case "exec-server":
		core.NewServer(act.Port, false, "", false, false, false, true).Start()
	case "exec-client":
		core.NewClient(act.Host, act.Port, false, 5, false, false, true).Connect()
	case "stream-send":
		core.NewServer(act.Port, false, "", false, false, true, false).Start()
	case "stream-recv":
		core.NewClient(act.Host, act.Port, false, 5, false, true, false).Connect()
	}
}

// ── CLI action handler ────────────────────────────────────────────────────────

func handleActions(args []string) error {
	target := host
	if len(args) > 0 {
		target = args[0]
	}

	switch {
	case listen && ttyMode:
		core.NewServer(port, udp, "", false, keepAlive, false, false).WithRemoteShell(true).Start()

	case listen:
		core.NewServer(port, udp, execute, shell, keepAlive, streaming, execMode).Start()

	case scan:
		sc := scanner.New(scanner.ScannerConfig{
			Timeout: time.Duration(timeout) * time.Second,
			Verbose: verbose,
			Version: version,
		})
		if scanRange != "" {
			if _, err := sc.ScanRange(scanRange, scanPorts); err != nil {
				return fmt.Errorf("scan range: %w", err)
			}
		} else if target != "" {
			if _, err := sc.ScanHost(target, scanPorts); err != nil {
				return fmt.Errorf("scan host: %w", err)
			}
		} else {
			return fmt.Errorf("scan requires a target host or --range")
		}

	case target != "" && ttyMode:
		core.NewClient(target, port, udp, timeout, keepAlive, false, false).WithRemoteShell(true).Connect()

	case target != "":
		c := core.NewClient(target, port, udp, timeout, keepAlive, streaming, execMode)
		if uploadFile != "" {
			c.WithFileSend(uploadFile)
		}
		if recvFile {
			c.WithFileRecv(true)
		}
		c.Connect()

	default:
		return fmt.Errorf("nothing to do — run 'ncCmdExe --help'")
	}

	return nil
}
