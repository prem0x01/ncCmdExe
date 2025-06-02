package main

import (
	"log"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/prem0x01/ncCmdExe/cmd"
	"github.com/prem0x01/ncCmdExe/internal/core"
	"github.com/prem0x01/ncCmdExe/internal/scanner"
	"github.com/prem0x01/ncCmdExe/internal/ui"
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#245465")).
			Padding(0, 1).
			Bold(true)

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Bold(true)
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
