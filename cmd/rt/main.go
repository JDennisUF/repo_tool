package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"repo_tool/internal/ui"
)

func main() {
	p := tea.NewProgram(ui.NewModel(), tea.WithAltScreen(), tea.WithANSICompressor())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
