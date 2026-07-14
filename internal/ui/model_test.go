package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLabelValuePadsToWidth(t *testing.T) {
	m := NewModel()
	line := m.labelValue("Status", "current", 40)

	if got := lipgloss.Width(line); got != 40 {
		t.Fatalf("labelValue width = %d, want 40", got)
	}
}

func TestLabelValueUsesStableBlankPrefix(t *testing.T) {
	m := NewModel()
	line := m.labelValue("", "origin/main", 40)

	if got := lipgloss.Width(line); got != 40 {
		t.Fatalf("labelValue width = %d, want 40", got)
	}
	if !strings.Contains(line, "origin/main") {
		t.Fatalf("labelValue line %q does not contain value", line)
	}
}
