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

var (
	listen    bool
	port      int
	host      string
	udp       bool
	execute   string
	shell     bool
	scan      bool
	scanPorts string
	scanRange string
	version   bool
	verbose   bool
	timeout   int
	keepAlive bool
)

var rootCmd = &cobra.Command{
	Use:   "ncCmdExe",
	Short: "NetCat with  Command Execution!",
	Long: `

                      /$$$$$$                      /$$ /$$$$$$$$
                     /$$__  $$                    | $$| $$_____/
 /$$$$$$$   /$$$$$$$| $$  \__/ /$$$$$$/$$$$   /$$$$$$$| $$       /$$   /$$  /$$$$$$
| $$__  $$ /$$_____/| $$      | $$_  $$_  $$ /$$__  $$| $$$$$   |  $$ /$$/ /$$__  $$
| $$  \ $$| $$      | $$      | $$ \ $$ \ $$| $$  | $$| $$__/    \  $$$$/ | $$$$$$$$
| $$  | $$| $$      | $$    $$| $$ | $$ | $$| $$  | $$| $$        >$$  $$ | $$_____/
| $$  | $$|  $$$$$$$|  $$$$$$/| $$ | $$ | $$|  $$$$$$$| $$$$$$$$ /$$/\  $$|  $$$$$$$
|__/  |__/ \_______/ \______/ |__/ |__/ |__/ \_______/|________/|__/  \__/ \_______/

	Developed by : prem0x01  


	Features:
		[*] Listen and Connect
		[*] Command Execution & Shell access
		[*] Port & Service sacnning with version detection
	`,

	Run: func(cmd *cobra.Command, args []string) {
		if !listen && !scan && execute == "" && len(args) == 0 {
			cmd.Help()
			return
		}
		if !listen && !scan && execute == "" && len(args) == 1 {
			startUIWithConnect(args[0])
			return
		}

		handleActions(args)
	},
}

func init() {
	rootCmd.Flags().BoolVarP(&listen, "listen", "l", false, "Listen for incomming connections")
	rootCmd.Flags().IntVarP(&port, "port", "p", 8080, "Port number")
	rootCmd.Flags().StringVarP(&host, "host", "H", "localhost", "Host address")
	rootCmd.Flags().BoolVarP(&udp, "udp", "u", false, "Use UDP insted of TCP")
	rootCmd.Flags().StringVarP(&execute, "execute", "e", "", "Execute command")
	rootCmd.Flags().BoolVarP(&shell, "shell", "s", false, "Enable shell mode")
	rootCmd.Flags().BoolVarP(&scan, "scan", "S", false, "Enable port scanning")
	rootCmd.Flags().StringVar(&scanPorts, "ports", "1-1000", "Ports to scan (e.g., 80,443 or 1-1000)")
	rootCmd.Flags().StringVar(&scanRange, "range", "", "IP range to scan")
	rootCmd.Flags().BoolVarP(&version, "version-scan", "v", false, "Enable version detection")
	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "Verbose output")
	rootCmd.Flags().IntVarP(&timeout, "timeout", "t", 5, "Connection timeout in seconds")
	rootCmd.Flags().BoolVarP(&keepAlive, "keep-alive", "k", false, "Keep connection alive")
}

func Execute() error {
	return rootCmd.Execute()
}

func startUIWithConnect(hostPort string) {
	m := ui.NewModel()
	m.StateToConnect(hostPort)

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

/*
	func startUI() {
		m := ui.NewModel()
		p := tea.NewProgram(m, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Printf("Error running TUI: %v\n", err)
			os.Exit(1)
		}
	}
*/
func handleActions(args []string) {
	if listen {
		server := core.NewServer(port, udp, execute, shell)
		server.Start()
	} else if scan {
		scanner := scanner.New(scanner.ScannerConfig{
			Timeout: time.Second * 5,
			Verbose: true,
			Version: true,
		})
		if scanRange != "" {
			scanner.ScanRange(scanRange, scanPorts)
		} else if len(args) > 0 {
			scanner.ScanHost(args[0], scanPorts)
		}
	} else if len(args) > 0 {
		client := core.NewClient(args[0], port, udp, timeout)
		client.Connect()
	}
}
