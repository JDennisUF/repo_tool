package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"repo_tool/internal/gitutil"
	"repo_tool/internal/store"
)

func TestLabelValueUsesFullAvailableValueWidth(t *testing.T) {
	m := NewModel()
	line := m.labelValue("Status", "current", 40)

	if got := lipgloss.Width(line); got != 40 {
		t.Fatalf("labelValue width = %d, want 40", got)
	}
}

func TestBuildReposContentShowsActiveRepoMarker(t *testing.T) {
	themes := loadThemeSet()
	m := Model{
		repos: []store.Repo{
			{Name: "repo-one", Path: "/tmp/repo-one", Selected: true},
		},
		activeFavoriteList: defaultFavoriteListName,
		activeRepoOps: map[string]struct{}{
			"/tmp/repo-one": {},
		},
		theme: themes.Themes[themes.Active],
	}

	content := m.buildReposContent(120, 5)

	if !strings.Contains(content, "[x] ~") {
		t.Fatalf("expected active repo marker in content, got %q", content)
	}
}

func TestPullFinishedClearsActiveRepoMarkers(t *testing.T) {
	themes := loadThemeSet()
	m := Model{
		repos: []store.Repo{
			{Name: "repo-one", Path: "/tmp/repo-one"},
		},
		activeFavoriteList: defaultFavoriteListName,
		activeRepoOps: map[string]struct{}{
			"/tmp/repo-one": {},
		},
		repoMeta: map[string]gitutil.RepoMetadata{},
		theme:    themes.Themes[themes.Active],
	}

	updated, _ := m.Update(repoOpEventMsg{
		event: repoOpEvent{
			kind: repoActionPull,
			result: pullResult{
				path:   "/tmp/repo-one",
				output: "ok",
			},
		},
	})
	got := updated.(Model)

	if got.repoOpActive(got.repos[0]) {
		t.Fatalf("expected active repo marker to clear after pull finishes")
	}
}
