package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/prem0x01/ncCmdExe/internal/scanner"
)

// ── session states ────────────────────────────────────────────────────────────

type sessionState int

const (
	menuView sessionState = iota
	remoteShellClientView // connect to a remote shell server (raw TTY)
	remoteShellServerView // serve a PTY shell to incoming connections
	connectView
	listenView
	shellView
	execServerView // server: listen for exec REPL clients
	execClientView // client: connect to exec REPL server
	streamSendView // server: stream screen
	streamRecvView // client: view stream
	scanView
	helpView
)

// ── styles ────────────────────────────────────────────────────────────────────
// All white — no color codes; terminal default foreground throughout.

var (
	boldStyle   = lipgloss.NewStyle().Bold(true)
	faintStyle  = lipgloss.NewStyle().Faint(true)
	italicFaint = lipgloss.NewStyle().Italic(true).Faint(true)
	normalStyle = lipgloss.NewStyle()
)

const bannerASCII = `                      /$$$$$$                      /$$ /$$$$$$$$
                     /$$__  $$                    | $$| $$_____/
 /$$$$$$$   /$$$$$$$| $$  \__/ /$$$$$$/$$$$   /$$$$$$$| $$       /$$   /$$  /$$$$$$
| $$__  $$ /$$_____/| $$      | $$_  $$_  $$ /$$__  $$| $$$$$   |  $$ /$$/ /$$__  $$
| $$  \ $$| $$      | $$      | $$ \ $$ \ $$| $$  | $$| $$__/    \  $$$$/ | $$$$$$$$
| $$  | $$| $$      | $$    $$| $$ | $$ | $$| $$  | $$| $$        >$$  $$ | $$_____/
| $$  | $$|  $$$$$$$|  $$$$$$/| $$ | $$ | $$|  $$$$$$$| $$$$$$$$ /$$/\  $$|  $$$$$$$
|__/  |__/ \_______/ \______/ |__/ |__/ |__/ \_______/|________/|__/  \__/ \_______/`

func renderBanner() string {
	lines := strings.Split(bannerASCII, "\n")
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(boldStyle.Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

// ── action ────────────────────────────────────────────────────────────────────

// Action is returned to the caller after the TUI exits.
type Action struct {
	Kind string
	Host string
	Port int
}

// ── model ─────────────────────────────────────────────────────────────────────

type menuItem struct {
	title string
	desc  string
	state sessionState
}

type Model struct {
	state     sessionState
	cursor    int
	spinner   spinner.Model
	textInput textinput.Model
	isLoading bool
	errMsg    string
	result    string
	menuItems []menuItem
	action    Action
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))

	ti := textinput.New()
	ti.Placeholder = "type here…"
	ti.Focus()

	return Model{
		state:     menuView,
		spinner:   s,
		textInput: ti,
		menuItems: []menuItem{
			{
				"Connect to a remote terminal",
				"Gives you a live shell on another machine — arrow keys, tab, vim all work.\n     The other machine must be running \"Share this machine's terminal\" first.",
				remoteShellClientView,
			},
			{
				"Share this machine's terminal  [!]",
				"Opens a port. The person who connects gets a full shell on THIS machine.\n     Only run this on machines you own and control.",
				remoteShellServerView,
			},
			{
				"Send / receive raw data",
				"Opens a direct TCP pipe to another host:port.\n     Anything you type is sent; anything sent back is shown.",
				connectView,
			},
			{
				"Wait for an incoming connection",
				"Opens a local port and waits. When someone connects, their data\n     is piped to your terminal.",
				listenView,
			},
			{
				"Run commands on a remote machine  (server side)",
				"Waits for a command client to connect. Each command it sends runs\n     here and output goes back. Supports file upload and download.",
				execServerView,
			},
			{
				"Send commands to a remote machine  (client side)",
				"Connects to a command server. Type a command, get the output.\n     !upload <file>   !download <file>   !exit",
				execClientView,
			},
			{
				"Share my screen",
				"Streams your screen to whoever connects next.\n     They watch it live in a browser window.",
				streamSendView,
			},
			{
				"Watch a remote screen",
				"Connects to a machine that is sharing its screen.\n     A browser window opens with the live view.",
				streamRecvView,
			},
			{
				"Scan open ports on a target",
				"Enter a hostname or IP. Ports 1-1000 are tested;\n     open ones are listed with the service name.",
				scanView,
			},
			{"Help — what does each option do?", "Show a plain-English explanation of every mode.", helpView},
			{"Exit", "", menuView},
		},
	}
}

func (m Model) Action() Action { return m.action }

// StateToConnect pre-fills the connect screen (used by CLI).
func (m *Model) StateToConnect(hostPort string) {
	m.state = connectView
	m.textInput.Placeholder = placeholderFor(connectView)
	m.textInput.SetValue(hostPort)
	m.textInput.Focus()
}

// ── tea interface ─────────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd { return m.spinner.Tick }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case menuView:
			return m.updateMenu(msg)
		case helpView:
			m.state = menuView
			return m, nil
		default:
			return m.updateInput(msg)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scanResultMsg:
		m.isLoading = false
		if len(msg.results) == 0 {
			m.result = "No open ports found."
		} else {
			var b strings.Builder
			fmt.Fprintf(&b, "Found %d open port(s):\n\n", len(msg.results))
			for _, r := range msg.results {
				fmt.Fprintf(&b, "  %d/tcp   %-16s %s\n", r.Port, r.Service, r.Version)
			}
			m.result = b.String()
		}
		return m, nil

	case errorMsg:
		m.isLoading = false
		m.errMsg = msg.err
		return m, nil
	}

	return m, nil
}

func (m Model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.menuItems)-1 {
			m.cursor++
		}
	case "?":
		m.state = helpView
	case "enter", " ":
		item := m.menuItems[m.cursor]
		if item.title == "Exit" {
			return m, tea.Quit
		}
		m.state = item.state
		m.errMsg = ""
		m.result = ""
		m.textInput.SetValue("")
		m.textInput.Placeholder = placeholderFor(item.state)
		m.textInput.Focus()
	}
	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.state = menuView
		m.errMsg = ""
		m.result = ""
		m.isLoading = false
		return m, nil
	case "enter":
		return m.handleAction()
	}
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m Model) handleAction() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textInput.Value())
	if input == "" {
		m.errMsg = "Please enter a value first."
		return m, nil
	}
	m.errMsg = ""
	m.result = ""

	switch m.state {
	case remoteShellClientView:
		h, p, err := parseHostPort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "remote-shell-client", Host: h, Port: p}
		return m, tea.Quit

	case remoteShellServerView:
		p, err := parsePort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "remote-shell-server", Port: p}
		return m, tea.Quit

	case connectView:
		h, p, err := parseHostPort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "connect", Host: h, Port: p}
		return m, tea.Quit

	case listenView:
		p, err := parsePort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "listen", Port: p}
		return m, tea.Quit

	case shellView:
		p, err := parsePort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "shell", Port: p}
		return m, tea.Quit

	case execServerView:
		p, err := parsePort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "exec-server", Port: p}
		return m, tea.Quit

	case execClientView:
		h, p, err := parseHostPort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "exec-client", Host: h, Port: p}
		return m, tea.Quit

	case streamSendView:
		p, err := parsePort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "stream-send", Port: p}
		return m, tea.Quit

	case streamRecvView:
		h, p, err := parseHostPort(input)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.action = Action{Kind: "stream-recv", Host: h, Port: p}
		return m, tea.Quit

	case scanView:
		m.isLoading = true
		return m, m.doScan(input)
	}

	return m, nil
}

// doScan runs a port scan asynchronously.
func (m Model) doScan(target string) tea.Cmd {
	return func() tea.Msg {
		sc := scanner.New(scanner.ScannerConfig{
			Timeout:        3 * time.Second,
			Verbose:        false,
			Version:        true,
			SKipHostDomain: true,
		})
		results := sc.ScanHostWithResults(target, "1-1000")
		return scanResultMsg{results: results}
	}
}

type scanResultMsg struct{ results []scanner.ScanResult }
type errorMsg struct{ err string }

// ── helpers ───────────────────────────────────────────────────────────────────

func placeholderFor(state sessionState) string {
	switch state {
	case connectView, execClientView, streamRecvView, remoteShellClientView:
		return "host:port  e.g.  10.0.0.5:4444"
	case listenView, shellView, execServerView, streamSendView, remoteShellServerView:
		return "port  e.g.  4444"
	case scanView:
		return "target  e.g.  scanme.nmap.org  or  192.168.1.1"
	}
	return "type here…"
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid port number (e.g. 4444)", s)
	}
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port must be 1–65535 (got %d)", p)
	}
	return p, nil
}

func parseHostPort(s string) (string, int, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("missing port — use host:port, e.g. example.com:80")
	}
	h := strings.TrimSpace(s[:i])
	if h == "" {
		return "", 0, fmt.Errorf("missing host — use host:port, e.g. example.com:80")
	}
	p, err := parsePort(s[i+1:])
	if err != nil {
		return "", 0, err
	}
	return h, p, nil
}

// ── views ─────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	switch m.state {
	case menuView:
		return m.viewMenu()
	case helpView:
		return m.viewHelp()
	default:
		return m.viewInput()
	}
}

func (m Model) viewMenu() string {
	var b strings.Builder

	b.WriteString(renderBanner())
	b.WriteString(faintStyle.Render("  by prem0x01   —   netcat-style remote access toolkit"))
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("  ──────────────────────────────────────────────────────────────────────────────"))
	b.WriteString("\n\n")

	for i, item := range m.menuItems {
		num := fmt.Sprintf("%2d.", i+1)
		if m.cursor == i {
			fmt.Fprintf(&b, " %s %s %s\n",
				boldStyle.Render("▶"),
				boldStyle.Render(num),
				boldStyle.Render(item.title),
			)
			if item.desc != "" {
				fmt.Fprintf(&b, "        %s\n", italicFaint.Render(item.desc))
			}
			b.WriteString("\n")
		} else {
			fmt.Fprintf(&b, "    %s %s\n", faintStyle.Render(num), normalStyle.Render(item.title))
		}
	}

	b.WriteString(faintStyle.Render("  ──────────────────────────────────────────────────────────────────────────────"))
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("  ↑ / ↓   move     Enter   select     q   quit"))
	return b.String()
}

func (m Model) viewInput() string {
	var b strings.Builder

	type inputMeta struct {
		title   string
		what    string // one sentence: what this mode does
		how     string // what value to enter
		example string
		warn    string
	}
	var meta inputMeta
	switch m.state {
	case remoteShellClientView:
		meta = inputMeta{
			title:   "Connect to a remote terminal",
			what:    "You will get a full interactive shell on the remote machine. Arrow keys, tab-complete, vim, htop — everything works. Press Ctrl+] at any time to disconnect.",
			how:     "Enter the address and port of the machine you want to connect to:",
			example: "10.0.0.5:4444",
		}
	case remoteShellServerView:
		meta = inputMeta{
			title:   "Share this machine's terminal",
			what:    "This machine will listen on the port you choose. When someone connects using \"Connect to a remote terminal\", they get a full shell here.",
			how:     "Enter the port number this machine should listen on:",
			example: "4444",
			warn:    "! Anyone who connects to this port gets full control of this machine. Only run this on machines you own.",
		}
	case connectView:
		meta = inputMeta{
			title:   "Send / receive raw data",
			what:    "Opens a plain TCP connection. Everything you type is sent to the other machine; everything it sends back is shown here.",
			how:     "Enter the address and port to connect to:",
			example: "scanme.nmap.org:80   or   10.0.0.5:4444",
		}
	case listenView:
		meta = inputMeta{
			title:   "Wait for an incoming connection",
			what:    "Opens a port on this machine and waits. When someone connects, their data is piped directly to your terminal.",
			how:     "Enter the port number to listen on:",
			example: "4444",
		}
	case shellView:
		meta = inputMeta{
			title:   "Share this terminal (legacy mode)",
			what:    "Listens on a port. The first person to connect gets a full interactive shell on this machine.",
			how:     "Enter the port number to listen on:",
			example: "4444",
			warn:    "! Anyone who connects gets a shell. Only run on machines you own.",
		}
	case execServerView:
		meta = inputMeta{
			title:   "Run commands on this machine  (server side)",
			what:    "Listens for a command client. When it connects, it can send commands one at a time. Each runs here and the output goes back. File upload/download are supported.",
			how:     "Enter the port number to listen on:",
			example: "4444",
			warn:    "! Anyone who connects can run arbitrary commands on this machine.",
		}
	case execClientView:
		meta = inputMeta{
			title:   "Send commands to a remote machine  (client side)",
			what:    "Connects to a command server. You type one command at a time and see the output. To transfer files: type  !upload <local-file>  or  !download <remote-file>. Type  !exit  to quit.",
			how:     "Enter the address and port of the command server:",
			example: "10.0.0.5:4444",
		}
	case streamSendView:
		meta = inputMeta{
			title:   "Share my screen",
			what:    "Listens for a viewer to connect. Once connected, your screen is streamed to them as a live video in their browser. Everything on your screen is visible.",
			how:     "Enter the port number to listen on:",
			example: "4444",
			warn:    "! Your entire screen will be visible to whoever connects.",
		}
	case streamRecvView:
		meta = inputMeta{
			title:   "Watch a remote screen",
			what:    "Connects to a machine that is sharing its screen. A browser window opens automatically with the live view.",
			how:     "Enter the address and port of the machine sharing its screen:",
			example: "10.0.0.5:4444",
		}
	case scanView:
		meta = inputMeta{
			title:   "Scan open ports on a target",
			what:    "Tests ports 1-1000 on the target machine and lists which ones are open, along with the service name (e.g. ssh, http).",
			how:     "Enter the hostname or IP address to scan:",
			example: "192.168.1.1   or   scanme.nmap.org",
		}
	}

	b.WriteString(boldStyle.Render(meta.title))
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("────────────────────────────────────────────────────────────────────────────────"))
	b.WriteString("\n\n")
	b.WriteString(meta.what)
	b.WriteString("\n\n")

	if meta.warn != "" {
		b.WriteString(boldStyle.Render(meta.warn))
		b.WriteString("\n\n")
	}

	b.WriteString(meta.how)
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("  e.g.  " + meta.example))
	b.WriteString("\n\n")

	b.WriteString(m.textInput.View())
	b.WriteString("\n\n")

	if m.isLoading {
		b.WriteString(m.spinner.View())
		b.WriteString("  Scanning…  this may take a few seconds.\n\n")
	}

	if m.errMsg != "" {
		b.WriteString(boldStyle.Render("Error: " + m.errMsg))
		b.WriteString("\n\n")
	}

	if m.result != "" {
		b.WriteString(m.result)
		b.WriteString("\n")
	}

	b.WriteString(faintStyle.Render("────────────────────────────────────────────────────────────────────────────────"))
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("Enter  confirm     Esc  go back     Ctrl+C  quit"))
	return b.String()
}

func (m Model) viewHelp() string {
	var b strings.Builder

	b.WriteString(boldStyle.Render("How to use ncCmdExe"))
	b.WriteString("\n")
	b.WriteString(faintStyle.Render("────────────────────────────────────────────────────────────────────────────────"))
	b.WriteString("\n\n")

	type entry struct{ title, body string }
	entries := []entry{
		{
			"Connect to a remote terminal  +  Share this machine's terminal",
			"These two options work as a pair.\n" +
				"  • Run \"Share\" on the machine you want to access (the target).\n" +
				"  • Run \"Connect\" on the machine you are sitting at.\n" +
				"  • You get a full interactive shell on the target — just like AnyDesk but in a terminal.\n" +
				"  • Press Ctrl+] to disconnect.",
		},
		{
			"Send / receive raw data  +  Wait for an incoming connection",
			"Plain TCP pipe — no shell, no framing, just bytes.\n" +
				"  • \"Wait\" opens a local port. \"Send/receive\" connects to it.\n" +
				"  • Useful for testing servers or moving data manually.",
		},
		{
			"Run commands (server + client)",
			"Like the remote terminal pair, but command-by-command — not a live shell.\n" +
				"  • Server listens; client connects and types one command at a time.\n" +
				"  • Output from each command is returned before the next prompt.\n" +
				"  • Supports file transfer:  !upload <file>   !download <file>   !exit",
		},
		{
			"Share my screen  +  Watch a remote screen",
			"Screen mirroring over TCP.\n" +
				"  • \"Share\" streams your desktop to whoever connects.\n" +
				"  • \"Watch\" connects and opens a browser with the live view.",
		},
		{
			"Scan open ports",
			"Finds which ports on a target machine are accepting connections.\n" +
				"  • Enter a hostname or IP (e.g. 192.168.1.1).\n" +
				"  • Ports 1-1000 are tested; open ones are listed with service names.",
		},
	}

	for _, e := range entries {
		b.WriteString(boldStyle.Render(e.title))
		b.WriteString("\n")
		b.WriteString(e.body)
		b.WriteString("\n\n")
	}

	b.WriteString(faintStyle.Render("Tip: you can also use flags — run   ncCmdExe --help   to see them all."))
	b.WriteString("\n\n")
	b.WriteString(faintStyle.Render("Press any key to go back."))
	return b.String()
}
