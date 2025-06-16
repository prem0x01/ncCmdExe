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
	"github.com/prem0x01/ncCmdExe/internal/core"
	"github.com/prem0x01/ncCmdExe/internal/scanner"
	//"github.com/ydb-platform/ydb-go-sdk/v3/table/result"
)

type sessionState int

const (
	menuView sessionState = iota
	listenView
	connectView
	scanView
	executeView
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#193373")).
			Padding(0, 1).
			Bold(true)

	menuStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5f56")).
			Bold(true).
			Underline(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5f34")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#28CA42")).
			Bold(true)
)

type Model struct {
	state     sessionState
	cursor    int
	selected  int
	spinner   spinner.Model
	textInput textinput.Model
	isLoading bool
	error     string
	result    string
	menuItems []string
}

func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#205"))

	ti := textinput.New()
	ti.Placeholder = "Enter value..."
	ti.Focus()

	return Model{
		state:     menuView,
		spinner:   s,
		textInput: ti,
		menuItems: []string{
			"[*] Start Server (Listen Mode)",
			"[*] Connect to Host",
			"[*] Port Scanner",
			"[*] Execute Command",
			"[*] Service Detection",
			"[*] Network Tools",
			"[*] Exit",
		},
	}
}

func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case menuView:
			return m.updateMenu(msg)
		case listenView, connectView, scanView, executeView:
			return m.updateInput(msg)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case serverStartedMsg:
		m.isLoading = false
		m.result = fmt.Sprintf("Server started successfully on port %d", msg.port)
		return m, nil

	case connectionSuccessMsg:
		m.isLoading = false
		m.result = fmt.Sprintf("Connected successfully to %s:%d", msg.host, msg.port)
		return m, nil

	case scanResultMsg:
		m.isLoading = false
		if len(msg.results) == 0 {
			m.result = "No open ports found"
		} else {
			resultStr := fmt.Sprintf("Found %d open ports:\n", len(msg.results))
			for _, result := range msg.results {
				resultStr += fmt.Sprintf("%d/tcp %s %s\n", result.Port, result.Service, result.Version)
			}
			m.result = resultStr
		}
		return m, nil

	case errorMsg:
		m.isLoading = false
		m.error = msg.err
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
	case "enter", " ":
		switch m.cursor {
		case 0:
			m.state = listenView
			m.textInput.Placeholder = "Enter port (default: 8080)..."
			m.textInput.SetValue("")
		case 1:
			m.state = connectView
			m.textInput.Placeholder = "Enter host:port (e.g., localhost:8080)..."
			m.textInput.SetValue("")
		case 2:
			m.state = scanView
			m.textInput.Placeholder = "Enter target (e.g., localhost or 192.168.1.1)..."
			m.textInput.SetValue("")
		case 3:
			m.state = executeView
			m.textInput.Placeholder = "Enter command to execute..."
			m.textInput.SetValue("")
		case 6:
			return m, tea.Quit
		}
		m.textInput.Focus()
	}
	return m, nil
}

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.state = menuView
		m.error = ""
		m.result = ""
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
		m.error = "Input cannot be empty"
		return m, nil
	}

	m.isLoading = true
	m.error = ""
	m.result = ""

	switch m.state {
	case listenView:
		return m, m.startServer(input)
	case connectView:
		return m, m.connectToHost(input)
	case scanView:
		return m, m.scanHost(input)
	case executeView:
		m.result = fmt.Sprintf("Command prepared: %s", input)
		m.isLoading = false
	}

	return m, nil
}

func (m Model) startServer(portStr string) tea.Cmd {
	return func() tea.Msg {
		port := 8080
		if portStr != "" {
			if p, err := strconv.Atoi(portStr); err == nil {
				port = p
			}
		}

		go func() {
			server := core.NewServer(port, false, "", false)
			server.Start()
		}()

		return serverStartedMsg{port: port}
	}
}

func (m Model) connectToHost(hostPort string) tea.Cmd {
	return func() tea.Msg {
		parts := strings.Split(hostPort, ":")
		if len(parts) != 2 {
			return errorMsg{err: "Invalid format. Use host:port"}
		}

		host := parts[0]
		port, err := strconv.Atoi(parts[1])
		if err != nil {
			return errorMsg{err: "Invalid port number"}
		}
		client := core.NewClient(host, port, false, 5)
		if err := client.TestConnection(); err != nil {
			return errorMsg{err: fmt.Sprintf("Connection failed: %v", err)}
		}

		return connectionSuccessMsg{host: host, port: port}
	}
}

func (m Model) scanHost(target string) tea.Cmd {
	return func() tea.Msg {
		scanner := scanner.New(scanner.ScannerConfig{
			Timeout: time.Second * 5,
			Verbose: true,
			Version: true,
		})
		results := scanner.ScanHostWithResults(target, "1-1000")
		return scanResultMsg{results: results}
	}
}

type serverStartedMsg struct{ port int }
type connectionSuccessMsg struct {
	host string
	port int
}
type scanResultMsg struct{ results []scanner.ScanResult }
type errorMsg struct{ err string }

func (m Model) View() string {
	switch m.state {
	case menuView:
		return m.menuView()
	default:
		return m.inputView()
	}
}

func (m Model) menuView() string {
	s := strings.Builder{}

	s.WriteString(titleStyle.Render("ncCmdExe"))
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("A go implementation of nc with command execution"))
	s.WriteString("\n\n")

	for i, item := range m.menuItems {
		cursor := " "
		if m.cursor == i {
			cursor = selectedStyle.Render("►")
			item = selectedStyle.Render(item)
		} else {
			item = menuStyle.Render(item)
		}
		s.WriteString(fmt.Sprintf("%s %s\n", cursor, item))
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("Use ↑/↓ arrows or j/k to navigate • Enter to select • q to quit"))

	return s.String()
}

func (m Model) inputView() string {
	s := strings.Builder{}

	var title string
	switch m.state {
	case listenView:
		title = "Server Setup"
	case connectView:
		title = "Connect to Host"
	case scanView:
		title = "Port Scanner"
	case executeView:
		title = "Execute Command"
	}

	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	s.WriteString(m.textInput.View())
	s.WriteString("\n\n")

	if m.isLoading {
		s.WriteString(m.spinner.View() + " Processing...")
		s.WriteString("\n\n")
	} else {
		s.WriteString(m.textInput.View())
		s.WriteString("\n\n")
	}

	if m.error != "" {
		s.WriteString(errorStyle.Render("X " + m.error))
		s.WriteString("\n\n")
	}

	if m.result != "" {
		s.WriteString(successStyle.Render(m.result))
		s.WriteString("\n\n")
	}

	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back • Ctrl+C to quit"))

	return s.String()

}

func (m *Model) StateToConnect(hostPort string) {
	m.state = connectView
	m.textInput.Placeholder = "Enter host:port (e.g., localhost:8080)..."
	m.textInput.SetValue(hostPort)
}

