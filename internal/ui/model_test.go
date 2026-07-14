package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLabelValueUsesFullAvailableValueWidth(t *testing.T) {
	m := NewModel()
	line := m.labelValue("Status", "current", 40)

	if got := lipgloss.Width(line); got != 40 {
		t.Fatalf("labelValue width = %d, want 40", got)
	}
}
