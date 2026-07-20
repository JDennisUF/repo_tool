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

func TestSettingsDialogShowsOpenShellInNewWindow(t *testing.T) {
	m := NewModel()
	m.settingsDraft.OpenShellInNewWindow = true

	view := m.settingsDialogView(80, 10)

	if !strings.Contains(view, "[x] Open Shell In New Window") {
		t.Fatalf("expected open shell in new window setting in view, got %q", view)
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

func TestRepoShellProcessUsesShellEnvAndRepoDir(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/bash")

	cmd, shellName, err := repoShellProcess("/tmp/repo-one")
	if err != nil {
		t.Fatalf("repoShellProcess returned error: %v", err)
	}
	if shellName != "bash" {
		t.Fatalf("shellName = %q, want bash", shellName)
	}
	if cmd.Path != "/usr/bin/bash" {
		t.Fatalf("cmd.Path = %q, want /usr/bin/bash", cmd.Path)
	}
	if cmd.Dir != "/tmp/repo-one" {
		t.Fatalf("cmd.Dir = %q, want /tmp/repo-one", cmd.Dir)
	}
}

func TestPowerShellDetection(t *testing.T) {
	for _, shell := range []string{"pwsh", "pwsh.exe", "powershell", "powershell.exe"} {
		if !isPowerShell(shell) {
			t.Fatalf("expected %q to be detected as PowerShell", shell)
		}
		if got := shellDisplayName(shell); got != "PowerShell" {
			t.Fatalf("shellDisplayName(%q) = %q, want PowerShell", shell, got)
		}
	}
}

func TestRepoShellProcessAddsPowerShellNoLogoFlag(t *testing.T) {
	t.Setenv("SHELL", "pwsh.exe")

	cmd, shellName, err := repoShellProcess("/tmp/repo-one")
	if err != nil {
		t.Fatalf("repoShellProcess returned error: %v", err)
	}
	if shellName != "PowerShell" {
		t.Fatalf("shellName = %q, want PowerShell", shellName)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "-NoLogo" {
		t.Fatalf("cmd.Args = %#v, want PowerShell with -NoLogo", cmd.Args)
	}
}

func TestRepoShellWindowProcessUsesWindowsStartForPowerShell(t *testing.T) {
	t.Setenv("SHELL", "pwsh.exe")

	cmd, shellName, err := repoShellWindowProcess(`C:\src\repo-one`, "windows")
	if err != nil {
		t.Fatalf("repoShellWindowProcess returned error: %v", err)
	}
	if shellName != "PowerShell" {
		t.Fatalf("shellName = %q, want PowerShell", shellName)
	}
	if cmd.Path != "cmd.exe" {
		t.Fatalf("cmd.Path = %q, want cmd.exe", cmd.Path)
	}
	wantArgs := []string{"cmd.exe", "/C", "start", "", "pwsh.exe", "-NoLogo"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if cmd.Dir != `C:\src\repo-one` {
		t.Fatalf("cmd.Dir = %q, want repo path", cmd.Dir)
	}
}
