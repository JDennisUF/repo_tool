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

func TestRepoInfoPanelDoesNotEllipsizePaddedLines(t *testing.T) {
	m := NewModel()
	m.repos = []store.Repo{
		{
			Name:          "repo-one",
			Path:          "/tmp/repo-one",
			GerritProject: "project-one",
			RemoteURL:     "origin",
		},
	}
	m.activeFavoriteList = defaultFavoriteListName
	m.cursor = 0

	body := m.buildRepoInfoContent(38, 6)
	panel := m.renderSection(-1, "Repo Info", body, 40, 8, false)

	if strings.Contains(panel, "...") {
		t.Fatalf("repo info panel should not ellipsize padded lines, got %q", panel)
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

func TestSettingsDialogShowsBulkConfirmation(t *testing.T) {
	m := NewModel()
	m.settingsDraft.BulkConfirmation = true

	view := m.settingsDialogView(80, 10)

	if !strings.Contains(view, "[x] Bulk Confirmation") {
		t.Fatalf("expected bulk confirmation setting in view, got %q", view)
	}
}

func TestShouldConfirmBulkGitActionUsesSetting(t *testing.T) {
	repos := []store.Repo{
		{Name: "one", Path: "/tmp/one"},
		{Name: "two", Path: "/tmp/two"},
	}
	m := Model{settings: store.Settings{BulkConfirmation: true}}
	if !m.shouldConfirmBulkGitAction(repos) {
		t.Fatal("expected bulk git action confirmation when setting is enabled")
	}
	if m.shouldConfirmBulkGitAction(repos[:1]) {
		t.Fatal("did not expect git action confirmation for a single repo")
	}

	m.settings.BulkConfirmation = false
	if m.shouldConfirmBulkGitAction(repos) {
		t.Fatal("did not expect bulk git action confirmation when setting is disabled")
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

func TestCycleLayoutModeCyclesThroughThreeStates(t *testing.T) {
	m := NewModel()

	m.cycleLayoutMode()
	if m.layoutMode != layoutOutputMaximized {
		t.Fatalf("first cycle layoutMode = %v, want %v", m.layoutMode, layoutOutputMaximized)
	}
	if m.focus != focusOutput {
		t.Fatalf("first cycle focus = %v, want %v", m.focus, focusOutput)
	}

	m.cycleLayoutMode()
	if m.layoutMode != layoutReposMaximized {
		t.Fatalf("second cycle layoutMode = %v, want %v", m.layoutMode, layoutReposMaximized)
	}
	if m.focus != focusRepos {
		t.Fatalf("second cycle focus = %v, want %v", m.focus, focusRepos)
	}

	m.cycleLayoutMode()
	if m.layoutMode != layoutNormal {
		t.Fatalf("third cycle layoutMode = %v, want %v", m.layoutMode, layoutNormal)
	}
}
