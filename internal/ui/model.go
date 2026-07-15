package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"
	"repo_tool/internal/discovery"
	"repo_tool/internal/gerrit"
	"repo_tool/internal/gitutil"
	"repo_tool/internal/store"
)

type inputMode int

const (
	inputNone inputMode = iota
	inputAddOne
	inputScan
	inputSearch
)

const defaultFavoriteListName = "default"

type focusSection int

const (
	focusRepos focusSection = iota
	focusOutput
)

const focusSectionCount = int(focusOutput) + 1

type searchScope int

const (
	searchScopeRepos searchScope = iota
	searchScopeThemes
	searchScopeOutput
)

type pullResult struct {
	path   string
	output string
	err    error
}

type pullFinishedMsg struct {
	results []pullResult
}

type fetchFinishedMsg struct {
	results []pullResult
}

type cloneFinishedMsg struct {
	results []pullResult
}

type gerritProjectsLoadedMsg struct {
	projects []string
	output   string
	err      error
}

type lazygitExitedMsg struct {
	err  error
	path string
}

type vscodeOpenedMsg struct {
	editor   string
	repoName string
	path     string
	err      error
}

// outputLine represents a single line in the command output panel.
type outputLine struct {
	ts   string
	text string
	fail bool
}

type Model struct {
	repos           []store.Repo
	cursor          int
	repoScroll      int
	width           int
	height          int
	status          string
	busy            bool
	showHelp        bool
	focus           focusSection
	settingsDialog  bool
	settingsCursor  int
	settingsDraft   store.Settings
	settingsEditing bool
	outputMaximized bool

	// output panel
	output    []outputLine
	outScroll int // index of first visible line

	store               *store.Store
	textInput           textinput.Model
	inputMode           inputMode
	searchScope         searchScope
	searchBackupQuery   string
	repoSearchQuery     string
	themeSearchQuery    string
	outputSearchQuery   string
	theme               themePalette
	themeName           string
	themes              map[string]themePalette
	themeNames          []string
	themeSelecting      bool
	themeCursor         int
	savedTheme          themePalette
	savedThemeName      string
	favoriteLists       map[string]map[string]struct{}
	activeFavoriteList  string
	favoritesOnly       bool
	favoritesDialog     bool
	favoritesDialogMode favoritesDialogMode
	favoritesListCursor int
	deleteConfirm       bool
	deleteConfirmRepo   store.Repo
	deleteConfirmLists  []string
	gerritDialog        bool
	gerritLoading       bool
	gerritProjects      []string
	gerritCursor        int
	gerritScroll        int
	gerritChecked       map[string]struct{}
	gerritSearching     bool
	gerritSearchQuery   string
	repoMeta            map[string]gitutil.RepoMetadata
	settings            store.Settings
}

type favoritesDialogMode int

const (
	favoritesDialogSelect favoritesDialogMode = iota
	favoritesDialogCreate
)

func NewModel() Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 2048
	ti.Width = 80
	themes := loadThemeSet()

	s, err := store.New()
	m := Model{
		store:      s,
		textInput:  ti,
		status:     "Ready",
		focus:      focusRepos,
		theme:      themes.Themes[themes.Active],
		themeName:  themes.Active,
		themes:     themes.Themes,
		themeNames: themes.Names,
	}
	if err != nil {
		m.status = fmt.Sprintf("Store init error: %v", err)
		return m
	}

	state, loadErr := s.Load()
	if loadErr != nil {
		m.status = fmt.Sprintf("Load error: %v", loadErr)
		return m
	}
	m.repos = state.Repos
	m.favoriteLists = favoriteListsFromState(state.FavoriteLists)
	m.activeFavoriteList = state.ActiveFavoriteList
	m.settings = state.Settings
	if m.activeFavoriteList == "" {
		m.activeFavoriteList = defaultFavoriteListName
	}
	if len(m.favoriteLists) == 0 {
		m.favoriteLists = map[string]map[string]struct{}{
			defaultFavoriteListName: {},
		}
	}
	if _, ok := m.favoriteLists[m.activeFavoriteList]; !ok {
		m.favoriteLists[m.activeFavoriteList] = map[string]struct{}{}
	}
	m.refreshRepoStatuses()
	m.logInfo(fmt.Sprintf("Loaded %d repositories", len(state.Repos)))
	return m
}

// logInfo appends an informational line to the output panel.
func (m *Model) logInfo(text string) {
	m.appendOutput(outputLine{ts: timestamp(), text: text})
}

// logSuccess appends a success line to the output panel.
func (m *Model) logSuccess(text string) {
	m.appendOutput(outputLine{ts: timestamp(), text: text, fail: false})
}

// logError appends a failure line to the output panel.
func (m *Model) logError(text string) {
	m.appendOutput(outputLine{ts: timestamp(), text: text, fail: true})
}

const maxOutputLines = 2000

func (m *Model) appendOutput(line outputLine) {
	m.output = append(m.output, line)
	if len(m.output) > maxOutputLines {
		m.output = m.output[len(m.output)-maxOutputLines:]
	}
}

// scrollToBottom pins the output panel view to the last line.
func (m *Model) scrollToBottom(visibleLines int) {
	if len(m.output) > visibleLines {
		m.outScroll = len(m.output) - visibleLines
	} else {
		m.outScroll = 0
	}
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) setFocus(focus focusSection) {
	m.focus = focus
	switch focus {
	case focusRepos:
		m.status = "Focused [0] Repos"
	case focusOutput:
		m.status = "Focused [1] Command Output"
	}
}

func (m *Model) cycleFocus(delta int) {
	next := (int(m.focus) + delta + focusSectionCount) % focusSectionCount
	m.setFocus(focusSection(next))
}

func (m Model) currentSearchScope() searchScope {
	if m.themeSelecting {
		return searchScopeThemes
	}
	if m.focus == focusOutput || m.outputMaximized {
		return searchScopeOutput
	}
	return searchScopeRepos
}

func (m Model) activeSearchQuery() string {
	switch m.searchScope {
	case searchScopeThemes:
		return m.themeSearchQuery
	case searchScopeOutput:
		return m.outputSearchQuery
	default:
		return m.repoSearchQuery
	}
}

func (m *Model) setActiveSearchQuery(value string) {
	switch m.searchScope {
	case searchScopeThemes:
		m.themeSearchQuery = value
		m.normalizeThemeCursor()
	case searchScopeOutput:
		m.outputSearchQuery = value
	default:
		m.repoSearchQuery = value
		m.normalizeCursor()
		m.ensureRepoCursorVisible(m.repoPanelContentRows())
	}
}

func (m *Model) openSearchMode() {
	m.searchScope = m.currentSearchScope()
	m.searchBackupQuery = m.activeSearchQuery()
	m.inputMode = inputSearch
	m.textInput.SetValue(m.searchBackupQuery)
	m.textInput.Focus()
	m.status = m.searchStatus(m.textInput.Value())
}

func (m Model) searchStatus(query string) string {
	return fmt.Sprintf("Search %s: %s", searchScopeLabel(m.searchScope), query)
}

func searchScopeLabel(scope searchScope) string {
	switch scope {
	case searchScopeThemes:
		return "themes"
	case searchScopeOutput:
		return "output"
	default:
		return "repos"
	}
}

func (m Model) titleWithSearch(title string, query string) string {
	if query == "" {
		return title
	}
	return fmt.Sprintf("%s /%s", title, query)
}

func containsFold(text string, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.Width = max(20, msg.Width/2-10)
		m.ensureRepoCursorVisible(m.repoPanelContentRows())
		return m, nil

	case pullFinishedMsg:
		m.busy = false
		successes := 0
		failures := 0
		for i := range m.repos {
			for _, r := range msg.results {
				if m.repoKey(m.repos[i]) != r.path {
					continue
				}
				if r.err != nil {
					m.repos[i].LastOp = "pull failed"
					failures++
					m.logError(fmt.Sprintf("[%s] FAIL: %s", m.repos[i].Name, r.output))
				} else {
					m.refreshRepoStatus(m.repos[i])
					newCount := m.syncRemoteBranchTracking(i)
					m.repos[i].LastOp = formatOpWithRemoteBranchDelta("pull ok", newCount)
					m.repos[i].LastUpdated = time.Now().Format(time.RFC3339)
					successes++
					m.logSuccess(fmt.Sprintf("[%s] OK: %s", m.repos[i].Name, r.output))
				}
			}
		}
		m.persist()
		summary := fmt.Sprintf("Pull complete: %d ok, %d failed", successes, failures)
		m.status = summary
		m.logInfo("--- " + summary + " ---")
		m.scrollToBottom(m.outPanelHeight())
		return m, nil

	case fetchFinishedMsg:
		m.busy = false
		successes := 0
		failures := 0
		for i := range m.repos {
			for _, r := range msg.results {
				if m.repoKey(m.repos[i]) != r.path {
					continue
				}
				if r.err != nil {
					m.repos[i].LastOp = "fetch failed"
					failures++
					m.logError(fmt.Sprintf("[%s] FAIL: %s", m.repos[i].Name, r.output))
				} else {
					m.refreshRepoStatus(m.repos[i])
					newCount := m.syncRemoteBranchTracking(i)
					m.repos[i].LastOp = formatOpWithRemoteBranchDelta("fetch ok", newCount)
					m.repos[i].LastUpdated = time.Now().Format(time.RFC3339)
					successes++
					m.logSuccess(fmt.Sprintf("[%s] OK: %s", m.repos[i].Name, r.output))
				}
			}
		}
		m.persist()
		summary := fmt.Sprintf("Fetch complete: %d ok, %d failed", successes, failures)
		m.status = summary
		m.logInfo("--- " + summary + " ---")
		m.scrollToBottom(m.outPanelHeight())
		return m, nil

	case cloneFinishedMsg:
		m.busy = false
		successes := 0
		failures := 0
		for i := range m.repos {
			for _, r := range msg.results {
				if m.repoKey(m.repos[i]) != r.path {
					continue
				}
				if r.err != nil {
					m.repos[i].LastOp = "clone failed"
					failures++
					m.logError(fmt.Sprintf("[%s] FAIL: %s", m.repos[i].Name, r.output))
				} else {
					m.repos[i].LastOp = "clone ok"
					m.repos[i].LastUpdated = time.Now().Format(time.RFC3339)
					successes++
					m.logSuccess(fmt.Sprintf("[%s] OK: %s", m.repos[i].Name, r.output))
					m.refreshRepoStatus(m.repos[i])
					newCount := m.syncRemoteBranchTracking(i)
					m.repos[i].LastOp = formatOpWithRemoteBranchDelta("clone ok", newCount)
				}
			}
		}
		m.persist()
		summary := fmt.Sprintf("Clone complete: %d ok, %d failed", successes, failures)
		m.status = summary
		m.logInfo("--- " + summary + " ---")
		m.scrollToBottom(m.outPanelHeight())
		return m, nil

	case gerritProjectsLoadedMsg:
		m.busy = false
		m.gerritLoading = false
		if msg.err != nil {
			m.gerritDialog = false
			m.status = fmt.Sprintf("Gerrit project load failed: %v", msg.err)
			m.logError("gerrit: " + msg.output)
			return m, nil
		}
		m.gerritDialog = true
		m.gerritProjects = msg.projects
		m.gerritCursor = 0
		m.gerritScroll = 0
		m.gerritChecked = map[string]struct{}{}
		m.gerritSearching = false
		m.gerritSearchQuery = ""
		m.status = fmt.Sprintf("Gerrit projects loaded: %d", len(msg.projects))
		m.logInfo(fmt.Sprintf("gerrit: loaded %d projects", len(msg.projects)))
		return m, nil

	case lazygitExitedMsg:
		if msg.path != "" {
			for _, repo := range m.repos {
				if m.repoKey(repo) == msg.path {
					m.refreshRepoStatus(repo)
					break
				}
			}
		}
		if msg.err != nil {
			m.status = fmt.Sprintf("lazygit failed: %v", msg.err)
			m.logError("lazygit: " + msg.err.Error())
		} else {
			m.status = "Returned from lazygit"
			m.logInfo("lazygit: session ended")
		}
		return m, nil

	case vscodeOpenedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("%s failed: %v", msg.editor, msg.err)
			m.logError(strings.ToLower(msg.editor) + ": " + msg.err.Error())
		} else {
			m.status = fmt.Sprintf("Opened %s: %s", msg.editor, msg.repoName)
			m.logInfo(fmt.Sprintf("%s: opened %s (%s)", strings.ToLower(msg.editor), msg.repoName, msg.path))
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}
		if m.inputMode == inputSearch {
			return m.handleInputMode(msg)
		}
		if m.gerritDialog {
			return m.handleGerritDialog(msg)
		}
		if m.deleteConfirm {
			return m.handleDeleteConfirm(msg)
		}
		if m.favoritesDialog {
			return m.handleFavoritesDialog(msg)
		}
		if m.settingsDialog {
			return m.handleSettingsDialog(msg)
		}
		if m.inputMode != inputNone {
			return m.handleInputMode(msg)
		}
		if msg.String() == "/" {
			m.openSearchMode()
			return m, nil
		}
		if m.themeSelecting {
			return m.handleThemeSelector(msg)
		}
		if m.showHelp {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "?", "esc", "enter":
				m.showHelp = false
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			m.searchScope = m.currentSearchScope()
			if m.activeSearchQuery() != "" {
				m.setActiveSearchQuery("")
				m.status = fmt.Sprintf("Cleared %s search", searchScopeLabel(m.searchScope))
			}
		case "0":
			m.setFocus(focusRepos)
		case "1":
			m.setFocus(focusOutput)
		case "left":
			m.cycleFocus(-1)
		case "right", "tab":
			m.cycleFocus(1)
		case "up", "k":
			if m.focus == focusOutput {
				m.outScroll = max(0, m.outScroll-1)
			} else {
				m.moveRepoCursor(-1)
			}
		case "down", "j":
			if m.focus == focusOutput {
				limit := max(0, len(m.output)-m.outPanelHeight())
				m.outScroll = min(m.outScroll+1, limit)
			} else {
				m.moveRepoCursor(1)
			}
		case "enter":
			if m.focus == focusRepos || m.focus == focusOutput {
				m.toggleOutputMaximized()
			}
		case " ":
			if idx, ok := m.currentRepoIndex(); ok {
				m.repos[idx].Selected = !m.repos[idx].Selected
				m.persist()
			}
		case "f":
			m.favoritesOnly = !m.favoritesOnly
			m.normalizeCursor()
			m.ensureRepoCursorVisible(m.repoPanelContentRows())
			if m.favoritesOnly {
				m.status = fmt.Sprintf("Favorites filter enabled: %s", m.activeFavoriteList)
			} else {
				m.status = "Favorites filter disabled"
			}
		case "F":
			if idx, ok := m.currentRepoIndex(); ok {
				repo := m.repos[idx]
				if m.toggleFavorite(m.repoKey(repo)) {
					m.status = fmt.Sprintf("Favorited %s in %s", repo.Name, m.activeFavoriteList)
					m.logInfo(fmt.Sprintf("favorite added: %s -> %s", repo.Name, m.activeFavoriteList))
				} else {
					m.status = fmt.Sprintf("Unfavorited %s from %s", repo.Name, m.activeFavoriteList)
					m.logInfo(fmt.Sprintf("favorite removed: %s -> %s", repo.Name, m.activeFavoriteList))
				}
				m.normalizeCursor()
				m.ensureRepoCursorVisible(m.repoPanelContentRows())
				m.persist()
			}
		case "r":
			if idx, ok := m.currentRepoIndex(); ok {
				repo := m.repos[idx]
				m.refreshRepoStatus(repo)
				newCount := m.syncRemoteBranchTracking(idx)
				m.status = fmt.Sprintf("Refreshed repo status: %s", repo.Name)
				if newCount > 0 {
					m.status = fmt.Sprintf("Refreshed repo status: %s (+%d remote branches)", repo.Name, newCount)
				}
				m.logInfo(fmt.Sprintf("refresh: %s (%s)", repo.Name, fallbackValue(repo.Path, repo.GerritProject)))
				m.persist()
			}
		case "R":
			m.refreshRepoStatuses()
			totalNewRemoteBranches := 0
			for i := range m.repos {
				totalNewRemoteBranches += m.syncRemoteBranchTracking(i)
			}
			m.status = fmt.Sprintf("Refreshed statuses for %d repositories", len(m.repos))
			if totalNewRemoteBranches > 0 {
				m.status = fmt.Sprintf("Refreshed statuses for %d repositories (+%d remote branches)", len(m.repos), totalNewRemoteBranches)
			}
			m.logInfo(fmt.Sprintf("refresh all: %d repositories", len(m.repos)))
			m.persist()
		case "a":
			visible := m.visibleRepoIndexes()
			for _, i := range visible {
				m.repos[i].Selected = true
			}
			m.persist()
			msg := fmt.Sprintf("Selected %d visible repositories", len(visible))
			m.status = msg
			m.logInfo(msg)
		case "A":
			for i := range m.repos {
				m.repos[i].Selected = false
			}
			m.persist()
			m.status = "Deselected all repositories"
			m.logInfo("Deselected all repositories")
		case "?":
			m.showHelp = !m.showHelp
		case "h":
			if m.busy {
				m.status = "Busy running fetch"
				return m, nil
			}
			targets := selectableRepos(selectedRepos(m.repos))
			scope := "selected repositories"
			if len(targets) == 0 {
				idx, ok := m.currentRepoIndex()
				if !ok {
					m.status = "No repository highlighted"
					m.logInfo("Fetch: no repository highlighted")
					return m, nil
				}
				if !m.hasLocalRepo(m.repos[idx]) {
					m.status = fmt.Sprintf("Repository not cloned: %s", m.repos[idx].Name)
					return m, nil
				}
				targets = []store.Repo{m.repos[idx]}
				scope = "highlighted repository"
			}
			m.busy = true
			m.status = fmt.Sprintf("Fetching %d repositories...", len(targets))
			m.logFetchStart(scope, targets)
			m.scrollToBottom(m.outPanelHeight())
			return m, fetchSelectedCmd(targets)
		case "c":
			if m.busy {
				m.status = "Busy running clone"
				return m, nil
			}
			targets := cloneableRepos(selectedRepos(m.repos))
			scope := "selected repositories"
			if len(targets) == 0 {
				idx, ok := m.currentRepoIndex()
				if !ok {
					m.status = "No repository highlighted"
					m.logInfo("Clone: no repository highlighted")
					return m, nil
				}
				target := m.repos[idx]
				if !m.isCloneableRepo(target) {
					m.status = fmt.Sprintf("Repository not cloneable: %s", target.Name)
					return m, nil
				}
				targets = []store.Repo{target}
				scope = "highlighted repository"
			}
			m.busy = true
			m.status = fmt.Sprintf("Cloning %d repositories...", len(targets))
			m.logCloneStart(scope, targets)
			m.scrollToBottom(m.outPanelHeight())
			return m, cloneSelectedCmd(targets)
		case "T":
			m.openThemeSelector()
		case "g":
			if m.busy {
				m.status = "Busy loading Gerrit projects"
				return m, nil
			}
			cfg := m.gerritConfig()
			if err := cfg.ValidateForListing(); err != nil {
				m.status = fmt.Sprintf("Gerrit settings incomplete: %v", err)
				return m, nil
			}
			m.busy = true
			m.gerritLoading = true
			m.status = "Loading Gerrit projects..."
			m.logInfo("gerrit: loading projects from " + cfg.Target())
			if m.settings.ShowGitCommands {
				m.logInfo("  $ " + gerrit.ListProjectsCommand(cfg.Target()))
			}
			return m, loadGerritProjectsCmd(cfg)
		case "l":
			m.openFavoritesDialog()
		case "S", ",":
			m.openSettingsDialog()
		case "+":
			m.toggleShowRepoInfo()
		case "o":
			m.inputMode = inputAddOne
			m.textInput.SetValue("")
			m.textInput.Focus()
			m.status = "Add repo: enter path"
			m.logInfo("Add repo: waiting for path input...")
		case "s":
			m.inputMode = inputScan
			m.textInput.SetValue("")
			m.textInput.Focus()
			m.status = "Scan for repos: enter root directory"
			m.logInfo("Scan: waiting for root directory input...")
		case "p":
			if m.busy {
				m.status = "Busy running pull"
				return m, nil
			}
			selected := selectableRepos(selectedRepos(m.repos))
			scope := "selected repositories"
			if len(selected) == 0 {
				idx, ok := m.currentRepoIndex()
				if !ok {
					m.status = "No repository highlighted"
					m.logInfo("Pull: no repository highlighted")
					return m, nil
				}
				if !m.hasLocalRepo(m.repos[idx]) {
					m.status = fmt.Sprintf("Repository not cloned: %s", m.repos[idx].Name)
					return m, nil
				}
				selected = []store.Repo{m.repos[idx]}
				scope = "highlighted repository"
			}
			m.busy = true
			m.status = fmt.Sprintf("Pulling %d repositories...", len(selected))
			m.logPullStart(scope, selected)
			m.scrollToBottom(m.outPanelHeight())
			return m, pullSelectedCmd(selected)
		case "x":
			idx, ok := m.currentRepoIndex()
			if !ok {
				return m, nil
			}
			repo := m.repos[idx]
			lists := m.favoriteListsContaining(m.repoKey(repo))
			if len(lists) > 0 {
				m.deleteConfirm = true
				m.deleteConfirmRepo = repo
				m.deleteConfirmLists = lists
				m.status = fmt.Sprintf("Confirm delete: %s", repo.Name)
				return m, nil
			}
			m.removeTrackedRepo(repo)
			return m, nil
		case "z":
			idx, ok := m.currentRepoIndex()
			if !ok {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[idx]
			if !m.hasLocalRepo(repo) {
				m.status = fmt.Sprintf("Repository not cloned: %s", repo.Name)
				return m, nil
			}
			cmd := exec.Command("lazygit")
			cmd.Dir = repo.Path
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			m.status = fmt.Sprintf("Launching lazygit: %s", repo.Name)
			m.logInfo(fmt.Sprintf("lazygit: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return lazygitExitedMsg{err: err, path: m.repoKey(repo)}
			})
		case "v":
			idx, ok := m.currentRepoIndex()
			if !ok {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[idx]
			if !m.hasLocalRepo(repo) {
				m.status = fmt.Sprintf("Repository not cloned: %s", repo.Name)
				return m, nil
			}
			m.status = fmt.Sprintf("Opening VS Code: %s", repo.Name)
			m.logInfo(fmt.Sprintf("code: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, openEditorCmd("VS Code", "code", repo)
		case "Z":
			idx, ok := m.currentRepoIndex()
			if !ok {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[idx]
			if !m.hasLocalRepo(repo) {
				m.status = fmt.Sprintf("Repository not cloned: %s", repo.Name)
				return m, nil
			}
			m.status = fmt.Sprintf("Opening Zed: %s", repo.Name)
			m.logInfo(fmt.Sprintf("zed: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, openEditorCmd("Zed", "zed", repo)
		// Output panel scrolling
		case "pgup":
			step := max(1, m.outPanelHeight()-1)
			m.outScroll = max(0, m.outScroll-step)
		case "pgdown":
			step := max(1, m.outPanelHeight()-1)
			limit := max(0, len(m.output)-m.outPanelHeight())
			m.outScroll = min(m.outScroll+step, limit)
		}

	case tea.MouseMsg:
		if msg.Type == tea.MouseLeft {
			if idx, ok := m.repoIndexAt(msg.X, msg.Y); ok {
				m.cursor = idx
				m.ensureRepoCursorVisible(m.repoPanelContentRows())
				m.setFocus(focusRepos)
				return m, nil
			}
		}
	}

	return m, nil
}

// outPanelHeight returns how many output lines are visible given current terminal height.
func (m *Model) outPanelHeight() int {
	bodyH := max(8, m.height-4)
	if m.outputMaximized {
		return max(1, bodyH-4)
	}
	if m.width < 64 {
		return max(1, bodyH-4)
	}
	topH := max(8, bodyH*2/3)
	outputH := max(5, bodyH-topH)
	return max(1, outputH-4)
}

func (m *Model) repoPanelContentRows() int {
	bodyH := max(8, m.height-4)
	topH := bodyH
	if m.width >= 64 {
		topH = max(8, bodyH*2/3)
	}
	rows := max(1, topH-2)
	if m.inputMode != inputNone {
		rows -= 3
	}
	if rows < 1 {
		rows = 1
	}
	return rows - 1 // reserve one row for the repo header
}

func (m Model) repoIndexAt(x int, y int) (int, bool) {
	if m.outputMaximized || m.showHelp || m.deleteConfirm || m.gerritDialog || m.favoritesDialog || m.settingsDialog || m.inputMode != inputNone {
		return 0, false
	}

	lw := m.leftWidth()
	bodyH := max(8, m.height-4)
	topH := bodyH
	if m.width >= 64 {
		topH = max(8, bodyH*2/3)
	}

	if x <= 0 || x >= lw-1 || y <= 0 || y >= topH-1 {
		return 0, false
	}

	contentRow := y - 1
	if contentRow == 0 {
		return 0, false
	}

	row := contentRow - 1
	visibleRows := m.repoPanelContentRows()
	if row < 0 || row >= visibleRows {
		return 0, false
	}

	visible := m.visibleRepoIndexes()
	if len(visible) == 0 {
		return 0, false
	}
	start, end := repoViewportRange(visible, m.repoScroll, visibleRows)
	if start+row >= end {
		return 0, false
	}

	return visible[start+row], true
}

// leftWidth and rightWidth split the top row with more room for the repo list.
func (m *Model) leftWidth() int {
	if m.width < 64 || !m.showRightColumn() {
		return m.width
	}
	return (m.width*2)/3 - 1
}

func (m *Model) rightWidth() int {
	if m.width < 64 || !m.showRightColumn() {
		return 0
	}
	return m.width - m.leftWidth() - 1
}

func (m Model) showRightColumn() bool {
	return m.settings.ShowRepoInfo || m.themeSelecting
}

func (m Model) repoMatchesSearch(repo store.Repo) bool {
	if m.repoSearchQuery == "" {
		return true
	}
	meta := m.repoMetadata(repo)
	haystack := strings.Join([]string{
		repo.Name,
		repo.Path,
		repo.GerritProject,
		repo.RemoteURL,
		meta.CurrentBranch,
		repo.LastOp,
	}, " ")
	return containsFold(haystack, m.repoSearchQuery)
}

func (m Model) visibleThemeNames() []string {
	if m.themeSearchQuery == "" {
		return m.themeNames
	}
	filtered := make([]string, 0, len(m.themeNames))
	for _, name := range m.themeNames {
		if containsFold(name, m.themeSearchQuery) {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func (m *Model) normalizeThemeCursor() {
	visible := m.visibleThemeNames()
	if len(visible) == 0 {
		m.themeCursor = 0
		return
	}
	if m.themeCursor < 0 {
		m.themeCursor = 0
	}
	if m.themeCursor >= len(visible) {
		m.themeCursor = len(visible) - 1
	}
	name := visible[m.themeCursor]
	if palette, ok := m.themes[name]; ok {
		m.theme = palette
		m.themeName = name
	}
}

func (m Model) highlightSearchMatches(text string, query string, baseStyle lipgloss.Style) string {
	if query == "" {
		return baseStyle.Render(text)
	}
	matchStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.Background)).
		Background(lipgloss.Color(m.theme.Accent)).
		Bold(true)
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	var b strings.Builder
	start := 0
	for {
		idx := strings.Index(lowerText[start:], lowerQuery)
		if idx < 0 {
			b.WriteString(baseStyle.Render(text[start:]))
			break
		}
		idx += start
		if idx > start {
			b.WriteString(baseStyle.Render(text[start:idx]))
		}
		end := idx + len(query)
		b.WriteString(matchStyle.Render(text[idx:end]))
		start = end
	}
	return b.String()
}

func (m Model) sectionStyle(width int, height int, focused bool) lipgloss.Style {
	borderColor := m.theme.Border
	if focused {
		borderColor = m.theme.BorderFocus
	}
	return lipgloss.NewStyle().
		Width(max(1, width-2)).
		Height(max(1, height-2)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		BorderBackground(lipgloss.Color(m.theme.Background)).
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Background(lipgloss.Color(m.theme.Background))
}

func (m Model) renderSection(number int, title string, body string, width int, height int, focused bool) string {
	borderColor := m.theme.Border
	if focused {
		borderColor = m.theme.BorderFocus
	}
	border := lipgloss.RoundedBorder()
	innerW := max(1, width-2)
	innerH := max(1, height-2)
	headerText := title
	if number >= 0 {
		headerText = fmt.Sprintf("[%d] %s", number, title)
	}
	header := m.fgStyle(m.theme.Header).
		Bold(true).
		Render(headerText)
	headerFill := max(0, innerW-lipgloss.Width(header))
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(borderColor)).
		Background(lipgloss.Color(m.theme.Background))
	top := borderStyle.Render(border.TopLeft) +
		header +
		borderStyle.Render(strings.Repeat(border.Top, headerFill)+border.TopRight)

	content := m.padBackground(m.indentBody(body, innerW), innerW, innerH)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = borderStyle.Render(border.Left) + line + borderStyle.Render(border.Right)
	}

	bottom := borderStyle.Render(border.BottomLeft + strings.Repeat(border.Bottom, innerW) + border.BottomRight)

	return strings.Join(append(append([]string{top}, lines...), bottom), "\n")
}

func (m *Model) refreshRepoStatuses() {
	if m.repoMeta == nil {
		m.repoMeta = make(map[string]gitutil.RepoMetadata, len(m.repos))
	}
	valid := make(map[string]struct{}, len(m.repos))
	for _, repo := range m.repos {
		key := m.repoKey(repo)
		if key == "" {
			continue
		}
		valid[key] = struct{}{}
		m.repoMeta[key] = m.inspectRepo(repo)
	}
	for path := range m.repoMeta {
		if _, ok := valid[path]; !ok {
			delete(m.repoMeta, path)
		}
	}
}

func (m *Model) refreshRepoStatus(repo store.Repo) {
	if m.repoMeta == nil {
		m.repoMeta = map[string]gitutil.RepoMetadata{}
	}
	key := m.repoKey(repo)
	if key == "" {
		return
	}
	m.repoMeta[key] = m.inspectRepo(repo)
}

func (m Model) repoStatus(repo store.Repo) gitutil.RepoStatus {
	if meta, ok := m.repoMeta[m.repoKey(repo)]; ok {
		return meta.Status
	}
	return gitutil.StatusNotCloned
}

func (m Model) repoMetadata(repo store.Repo) gitutil.RepoMetadata {
	if meta, ok := m.repoMeta[m.repoKey(repo)]; ok {
		return meta
	}
	return gitutil.RepoMetadata{Status: gitutil.StatusNotCloned}
}

func (m Model) repoKey(repo store.Repo) string {
	return store.RepoKey(repo)
}

func (m Model) inspectRepo(repo store.Repo) gitutil.RepoMetadata {
	if strings.TrimSpace(repo.Path) == "" {
		return gitutil.RepoMetadata{Status: gitutil.StatusNotCloned}
	}
	return gitutil.InspectRepoMetadata(repo.Path)
}

func (m *Model) syncRemoteBranchTracking(repoIndex int) int {
	if repoIndex < 0 || repoIndex >= len(m.repos) {
		return 0
	}
	repo := m.repos[repoIndex]
	meta := m.repoMetadata(repo)
	if meta.Status == gitutil.StatusNotCloned {
		return 0
	}
	newBranches := diffBranches(meta.RemoteBranches, repo.RemoteBranches)
	m.repos[repoIndex].RemoteBranches = append([]string(nil), meta.RemoteBranches...)
	m.repos[repoIndex].NewRemoteBranches = newBranches
	return len(newBranches)
}

func (m Model) hasLocalRepo(repo store.Repo) bool {
	return m.repoStatus(repo) != gitutil.StatusNotCloned
}

func (m Model) buildReposContent(width int, rows int) string {
	contentW := max(1, width-1)
	updatedW := 5
	syncW := 7
	ageW := 5
	const separatorCount = 7
	fixedW := 3 + 3 + 6 + syncW + updatedW + ageW + separatorCount
	visible := m.visibleRepoIndexes()
	nameW, branchW, authorW, opW := m.repoColumnWidths(max(0, contentW-fixedW), visible)
	header := padCell("Sel", 3) +
		" " + padCell("F", 3) +
		" " + padCell("Name ("+m.activeFavoriteList+")", nameW) +
		" " + padCell("St", 6) +
		padCell("↑↓", syncW) +
		padCell("Upd", updatedW) +
		" " + padCell("Branch", branchW) +
		" " + padCell("Age", ageW) +
		" " + padCell("Author", authorW) +
		" " + padCell("Op", opW)
	lines := []string{
		padStyledCell(
			m.fgStyle(m.theme.Muted).Render(trimRight(header, contentW)),
			contentW,
			m.theme.Background,
		),
	}

	if len(m.repos) == 0 {
		lines = append(lines, padStyledCell(m.fgStyle(m.theme.Muted).Render("(no repos; press o to add, s to scan, or g for Gerrit)"), contentW, m.theme.Background))
	} else if len(visible) == 0 {
		lines = append(lines, padStyledCell(m.fgStyle(m.theme.Muted).Render("(no matching repos)"), contentW, m.theme.Background))
	} else {
		availableRows := rows - len(lines)
		if m.inputMode != inputNone {
			availableRows -= 3
		}
		if availableRows < 1 {
			availableRows = 1
		}
		start, end := repoViewportRange(visible, m.repoScroll, availableRows)
		for _, idx := range visible[start:end] {
			repo := m.repos[idx]
			meta := m.repoMetadata(repo)
			status := meta.Status
			focused := idx == m.cursor && m.focus == focusRepos
			rowBg := m.theme.Background
			if focused {
				rowBg = m.theme.RowFocusBg
			}

			selStyle := m.fgBgStyle(m.theme.Foreground, rowBg)
			sel := "[ ]"
			if repo.Selected {
				selStyle = m.fgBgStyle(m.theme.Selection, rowBg).Bold(true)
				sel = "[x]"
			}
			if focused {
				selStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
			}

			favStyle := m.fgBgStyle(m.theme.Muted, rowBg)
			fav := " "
			if m.isFavorite(m.repoKey(repo)) {
				favStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
				fav = "*"
			}

			statusStyle := m.fgBgStyle(m.theme.Warning, rowBg).Bold(true)
			switch status {
			case gitutil.StatusCurrent:
				statusStyle = statusStyle.Foreground(lipgloss.Color(m.theme.Success))
			case gitutil.StatusUncommittedChanges:
				statusStyle = statusStyle.Foreground(lipgloss.Color(m.theme.Error))
			case gitutil.StatusUntrackedFiles:
				statusStyle = statusStyle.Foreground(lipgloss.Color(m.theme.Accent))
			default:
				statusStyle = statusStyle.Foreground(lipgloss.Color(m.theme.Muted))
			}

			nameStyle := m.fgBgStyle(m.theme.Foreground, rowBg)
			if focused {
				nameStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
			}
			branchStyle := m.fgBgStyle(m.theme.Foreground, rowBg)
			if focused {
				branchStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
			}
			authorStyle := m.fgBgStyle(m.theme.Foreground, rowBg)
			if focused {
				authorStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
			}
			opStyle := m.fgBgStyle(m.theme.Muted, rowBg)
			op := "-"
			if repo.LastOp != "" {
				op = repo.LastOp
				opStyle = m.fgBgStyle(m.theme.Warning, rowBg)
				if strings.Contains(repo.LastOp, "ok") {
					opStyle = opStyle.Foreground(lipgloss.Color(m.theme.Success))
				}
				if strings.Contains(repo.LastOp, "failed") {
					opStyle = opStyle.Foreground(lipgloss.Color(m.theme.Error))
				}
			}

			name := trimRight(repo.Name, nameW)
			branch := trimRight(meta.CurrentBranch, branchW)
			if branch == "" {
				branch = "-"
			}
			updated := formatLastUpdatedShort(repo.LastUpdated)
			commitAge := formatCommitAgeShort(meta.LastCommitAt)
			commitAuthor := trimRight(meta.LastCommitAuthor, authorW)
			if commitAuthor == "" {
				commitAuthor = "-"
			}
			sep := m.bgStyle().Render(" ")
			if focused {
				sep = m.bgStyle().Background(lipgloss.Color(rowBg)).Render(" ")
			}
			row := selStyle.Render(padCell(sel, 3)) +
				sep + favStyle.Render(padCell(fav, 3)) +
				sep + nameStyle.Render(padCell(name, nameW)) +
				sep + statusStyle.Render(padCell(status.Symbol(), 6)) +
				m.renderSyncCell(meta, rowBg, syncW) +
				m.fgBgStyle(m.theme.Muted, rowBg).Render(padCell(updated, updatedW)) +
				sep + branchStyle.Render(padCell(branch, branchW)) +
				sep + m.fgBgStyle(m.theme.Muted, rowBg).Render(padCell(commitAge, ageW)) +
				sep + authorStyle.Render(padCell(commitAuthor, authorW)) +
				sep + opStyle.Render(padCell(trimRight(op, opW), opW))
			lines = append(lines, padStyledCell(row, contentW, rowBg))
		}
	}

	if m.inputMode != inputNone && (m.inputMode != inputSearch || m.searchScope == searchScopeRepos) {
		prompt := "Path"
		if m.inputMode == inputScan {
			prompt = "Scan root"
		} else if m.inputMode == inputSearch {
			prompt = "Search"
		}
		input := trimRight(prompt+": "+m.textInput.View(), contentW)
		lines = append(lines,
			padStyledCell("", contentW, m.theme.Background),
			padStyledCell(m.fgStyle(m.theme.Input).Render(input), contentW, m.theme.Background),
			padStyledCell(m.fgStyle(m.theme.Muted).Render(trimRight("Enter=confirm Esc=cancel", contentW)), contentW, m.theme.Background),
		)
	}

	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) buildRepoInfoContent(width int, rows int) string {
	idx, ok := m.currentRepoIndex()
	if !ok || width < 10 {
		return padStyledCell(m.fgStyle(m.theme.Muted).Render("(no repo selected)"), width, m.theme.Background)
	}

	r := m.repos[idx]
	meta := m.repoMetadata(r)
	lastOp := r.LastOp
	if lastOp == "" {
		lastOp = "none"
	}
	favorite := "no"
	if m.isFavorite(m.repoKey(r)) {
		favorite = "yes"
	}
	status := meta.Status
	currentBranch := meta.CurrentBranch
	if currentBranch == "" {
		currentBranch = "(none)"
	}
	lastUpdated := formatLastUpdated(r.LastUpdated)
	lines := []string{
		m.labelValue("Name", r.Name, width),
		m.labelValue("Project", fallbackValue(r.GerritProject, "none"), width),
		m.labelValue("Remote", fallbackValue(r.RemoteURL, "none"), width),
		m.labelValue("Path", r.Path, width),
		m.labelValue("Branch", currentBranch, width),
		m.labelValue("Status", status.Description(), width),
		m.labelValue("Remote Refs", formatRemoteBranchCount(len(meta.RemoteBranches)), width),
		m.labelValue("New Remote", formatRemoteBranchCount(len(r.NewRemoteBranches)), width),
		m.labelValue("Commit Age", formatCommitAge(meta.LastCommitAt), width),
		m.labelValue("Commit By", fallbackValue(meta.LastCommitAuthor, "none"), width),
		m.labelValue("Updated", lastUpdated, width),
		m.labelValue("Last", lastOp, width),
		m.labelValue("Favorite", favorite, width),
		m.labelValue("Fav List", m.activeFavoriteList, width),
	}
	lines = append(lines, m.labelValue("Branches", "", width))
	if len(meta.LocalBranches) == 0 {
		lines = append(lines, m.labelValue("", "(none)", width))
	} else {
		for _, branch := range meta.LocalBranches {
			lines = append(lines, m.labelValue("", branch, width))
		}
	}
	if len(r.NewRemoteBranches) > 0 {
		lines = append(lines, m.labelValue("Remote Branches", "", width))
		for _, branch := range r.NewRemoteBranches {
			lines = append(lines, m.labelValue("", branch, width))
		}
	}
	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) buildOutputContent(width int, rows int) string {
	if rows <= 0 {
		return ""
	}
	visibleRows := max(1, rows-1)
	start := m.outScroll
	if start > len(m.output) {
		start = len(m.output)
	}
	end := min(start+visibleRows, len(m.output))
	lines := []string{}
	for _, ol := range m.output[start:end] {
		style := m.fgStyle(m.theme.Foreground)
		prefix := "  "
		if ol.fail {
			prefix = "! "
			style = style.Foreground(lipgloss.Color(m.theme.Error))
		}
		line := trimRight(prefix+ol.ts+" "+ol.text, width)
		lines = append(lines, m.highlightSearchMatches(line, m.outputSearchQuery, style))
	}
	if len(lines) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no command output yet)"))
	}
	if len(m.output) > 0 {
		indicator := fmt.Sprintf("[%d-%d / %d]", start+1, end, len(m.output))
		lines = append(lines, m.fgStyle(m.theme.Muted).Render(trimRight(indicator, width)))
	}
	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) View() string {
	lw := m.leftWidth()
	rw := m.rightWidth()
	bodyH := max(8, m.height-4)
	if m.outputMaximized {
		outputPanel := m.renderSection(1, m.titleWithSearch("Command Output", m.outputSearchQuery), m.buildOutputContent(max(1, m.width-2), max(1, bodyH-2)), m.width, bodyH, m.focus == focusOutput)
		body := outputPanel
		if m.showHelp {
			body = m.renderHelpOverlay(body)
		}
		if m.deleteConfirm {
			body = m.renderDeleteConfirmDialog(body)
		}
		if m.gerritDialog {
			body = m.renderGerritDialog(body)
		}
		if m.favoritesDialog {
			body = m.renderFavoritesDialog(body)
		}
		if m.settingsDialog {
			body = m.renderSettingsDialog(body)
		}

		busy := "idle"
		if m.busy {
			busy = "busy"
		}
		status := lipgloss.NewStyle().
			Foreground(lipgloss.Color(m.theme.StatusText)).
			Background(lipgloss.Color(m.theme.Status)).
			Width(max(1, m.width)).
			Render(" Status: " + m.status + " [" + busy + "] theme=" + m.themeName + " favorites=" + m.activeFavoriteList)
		keys := m.fgStyle(m.theme.Muted).
			Width(max(1, m.width)).
			Render(" [0]/[1] focus  /=search  Enter=max output  left/right cycle panels  +=repo info  f filter  F favorite  g gerrit  c clone  h fetch  l lists  S settings  r/R refresh  T themes  j/k move/scroll  space toggle  a/A sel/desel  o add  s scan  p pull  x remove  z lazygit  v code  Z zed  ? help  q quit")
		return m.renderApp(lipgloss.JoinVertical(lipgloss.Left, body, status, keys))
	}
	topH := bodyH
	outputH := 0
	if m.width >= 64 {
		topH = max(8, bodyH*2/3)
		outputH = max(5, bodyH-topH)
	}

	leftPanel := m.renderSection(0, m.titleWithSearch("Repos", m.repoSearchQuery), m.buildReposContent(max(1, lw-2), max(1, topH-2)), lw, topH, m.focus == focusRepos)

	body := leftPanel
	if rw > 0 {
		rightPanel := ""
		if m.settings.ShowRepoInfo {
			rightPanel = m.renderSection(-1, "Repo Info", m.buildRepoInfoContent(max(1, rw-2), max(1, topH-2)), rw, topH, false)
		}
		if m.themeSelecting && outputH > 0 {
			if m.settings.ShowRepoInfo {
				infoBody := m.buildRepoInfoContent(max(1, rw-2), max(1, bodyH-2))
				infoLines := len(strings.Split(infoBody, "\n"))
				infoH := min(max(8, infoLines+2), max(8, bodyH/2))
				themeH := max(5, bodyH-infoH)
				infoPanel := m.renderSection(-1, "Repo Info", m.buildRepoInfoContent(max(1, rw-2), max(1, infoH-2)), rw, infoH, false)
				themePanel := m.renderThemeSelector(rw, themeH)
				rightPanel = lipgloss.JoinVertical(lipgloss.Left, infoPanel, themePanel)
			} else {
				rightPanel = m.renderThemeSelector(rw, bodyH)
			}
			leftBottom := m.renderSection(1, m.titleWithSearch("Command Output", m.outputSearchQuery), m.buildOutputContent(max(1, lw-2), max(1, outputH-2)), lw, outputH, m.focus == focusOutput)
			leftColumn := lipgloss.JoinVertical(lipgloss.Left, leftPanel, leftBottom)
			gutter := m.renderGutter(bodyH)
			body = lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, gutter, rightPanel)
		} else {
			gutter := m.renderGutter(topH)
			topRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, gutter, rightPanel)
			outputPanel := m.renderSection(1, m.titleWithSearch("Command Output", m.outputSearchQuery), m.buildOutputContent(max(1, m.width-2), max(1, outputH-2)), m.width, outputH, m.focus == focusOutput)
			body = lipgloss.JoinVertical(lipgloss.Left, topRow, outputPanel)
		}
	} else if outputH > 0 {
		outputPanel := m.renderSection(1, m.titleWithSearch("Command Output", m.outputSearchQuery), m.buildOutputContent(max(1, m.width-2), max(1, outputH-2)), m.width, outputH, m.focus == focusOutput)
		body = lipgloss.JoinVertical(lipgloss.Left, leftPanel, outputPanel)
	}
	if m.showHelp {
		body = m.renderHelpOverlay(body)
	}
	if m.deleteConfirm {
		body = m.renderDeleteConfirmDialog(body)
	}
	if m.gerritDialog {
		body = m.renderGerritDialog(body)
	}
	if m.favoritesDialog {
		body = m.renderFavoritesDialog(body)
	}
	if m.settingsDialog {
		body = m.renderSettingsDialog(body)
	}

	// ---------- status bar ----------
	busy := "idle"
	if m.busy {
		busy = "busy"
	}
	status := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.StatusText)).
		Background(lipgloss.Color(m.theme.Status)).
		Width(max(1, m.width)).
		Render(" Status: " + m.status + " [" + busy + "] theme=" + m.themeName + " favorites=" + m.activeFavoriteList)
	keys := m.fgStyle(m.theme.Muted).
		Width(max(1, m.width)).
		Render(" [0]/[1] focus  /=search  Enter=max output  left/right cycle panels  +=repo info  f filter  F favorite  g gerrit  c clone  h fetch  l lists  S settings  r/R refresh  T themes  j/k move/scroll  space toggle  a/A sel/desel  o add  s scan  p pull  x remove  z lazygit  v code  Z zed  ? help  q quit")
	return m.renderApp(lipgloss.JoinVertical(lipgloss.Left, body, status, keys))
}

func (m Model) renderHelpOverlay(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(44, screenW-4), 96)
	dialogH := min(max(28, screenH-2), 40)
	dialog := m.renderSection(8, "Help", m.helpView(max(1, dialogW-4)), dialogW, dialogH, true)

	top := max(0, (screenH-dialogH)/2)
	left := max(0, (screenW-dialogW)/2)
	dialogLines := strings.Split(dialog, "\n")
	bg := m.bgStyle()
	lines := make([]string, screenH)
	for i := range lines {
		if i < top || i >= top+len(dialogLines) {
			lines[i] = bg.Render(strings.Repeat(" ", screenW))
			continue
		}
		dialogLine := dialogLines[i-top]
		right := max(0, screenW-left-lipgloss.Width(dialogLine))
		lines[i] = bg.Render(strings.Repeat(" ", left)) + dialogLine + bg.Render(strings.Repeat(" ", right))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderApp(content string) string {
	content = m.padBackground(content, max(1, m.width), max(1, m.height))
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Background(lipgloss.Color(m.theme.Background)).
		Width(max(1, m.width)).
		Height(max(1, m.height)).
		Render(content)
}

func (m Model) fgStyle(color string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Background(lipgloss.Color(m.theme.Background))
}

func (m Model) fgBgStyle(color string, bg string) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Background(lipgloss.Color(bg))
}

func (m Model) bgStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(m.theme.Background))
}

func (m Model) renderGutter(height int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = m.bgStyle().Render(" ")
	}
	return strings.Join(lines, "\n")
}

func (m Model) padBackground(content string, width int, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	fillStyle := m.bgStyle()
	for i, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth < width {
			line += fillStyle.Render(strings.Repeat(" ", width-lineWidth))
		}
		lines[i] = line
	}
	for len(lines) < height {
		lines = append(lines, fillStyle.Render(strings.Repeat(" ", width)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderThemeSelector(width int, height int) string {
	rows := max(1, height-2)
	lines := []string{}
	visible := m.visibleThemeNames()
	if len(visible) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no matching themes)"))
	} else {
		start := 0
		if m.themeCursor >= rows {
			start = m.themeCursor - rows + 1
		}
		end := min(start+rows, len(visible))
		for i := start; i < end; i++ {
			name := visible[i]
			marker := "  "
			style := m.fgStyle(m.theme.Foreground)
			if i == m.themeCursor {
				marker = "> "
				style = style.Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
			}
			if name == m.savedThemeName {
				name += " *"
			}
			lines = append(lines, style.Render(trimRight(marker+name, max(1, width-4))))
		}
	}
	footer := m.fgStyle(m.theme.Muted).Render("j/k preview  / search  Enter select  Esc cancel")
	body := strings.Join(append(lines, footer), "\n")
	return m.renderSection(-1, m.titleWithSearch("Theme Selector", m.themeSearchQuery), body, width, height, true)
}

func (m Model) renderFavoritesDialog(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(36, screenW-8), 76)
	dialogH := min(max(12, screenH-6), 22)
	dialog := m.renderSection(7, "Favorites Lists", m.favoritesDialogView(max(1, dialogW-4), max(1, dialogH-2)), dialogW, dialogH, true)

	top := max(0, (screenH-dialogH)/2)
	left := max(0, (screenW-dialogW)/2)
	dialogLines := strings.Split(dialog, "\n")
	bg := m.bgStyle()
	lines := make([]string, screenH)
	for i := range lines {
		if i < top || i >= top+len(dialogLines) {
			lines[i] = bg.Render(strings.Repeat(" ", screenW))
			continue
		}
		dialogLine := dialogLines[i-top]
		right := max(0, screenW-left-lipgloss.Width(dialogLine))
		lines[i] = bg.Render(strings.Repeat(" ", left)) + dialogLine + bg.Render(strings.Repeat(" ", right))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderGerritDialog(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(60, screenW-4), max(60, screenW))
	dialogH := min(max(18, screenH-2), max(18, screenH))
	dialog := m.renderSection(9, "Gerrit Projects", m.gerritDialogView(max(1, dialogW-4), max(1, dialogH-2)), dialogW, dialogH, true)

	top := max(0, (screenH-dialogH)/2)
	left := max(0, (screenW-dialogW)/2)
	dialogLines := strings.Split(dialog, "\n")
	bg := m.bgStyle()
	lines := make([]string, screenH)
	for i := range lines {
		if i < top || i >= top+len(dialogLines) {
			lines[i] = bg.Render(strings.Repeat(" ", screenW))
			continue
		}
		dialogLine := dialogLines[i-top]
		right := max(0, screenW-left-lipgloss.Width(dialogLine))
		lines[i] = bg.Render(strings.Repeat(" ", left)) + dialogLine + bg.Render(strings.Repeat(" ", right))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderDeleteConfirmDialog(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(44, screenW-8), 84)
	dialogH := min(max(12, screenH-6), 22)
	dialog := m.renderSection(-1, "Confirm Remove", m.deleteConfirmView(max(1, dialogW-4), max(1, dialogH-2)), dialogW, dialogH, true)

	top := max(0, (screenH-dialogH)/2)
	left := max(0, (screenW-dialogW)/2)
	dialogLines := strings.Split(dialog, "\n")
	bg := m.bgStyle()
	lines := make([]string, screenH)
	for i := range lines {
		if i < top || i >= top+len(dialogLines) {
			lines[i] = bg.Render(strings.Repeat(" ", screenW))
			continue
		}
		dialogLine := dialogLines[i-top]
		right := max(0, screenW-left-lipgloss.Width(dialogLine))
		lines[i] = bg.Render(strings.Repeat(" ", left)) + dialogLine + bg.Render(strings.Repeat(" ", right))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderSettingsDialog(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(38, screenW-8), 72)
	dialogH := min(max(10, screenH-6), 16)
	dialog := m.renderSection(-1, "Settings", m.settingsDialogView(max(1, dialogW-4), max(1, dialogH-2)), dialogW, dialogH, true)

	top := max(0, (screenH-dialogH)/2)
	left := max(0, (screenW-dialogW)/2)
	dialogLines := strings.Split(dialog, "\n")
	bg := m.bgStyle()
	lines := make([]string, screenH)
	for i := range lines {
		if i < top || i >= top+len(dialogLines) {
			lines[i] = bg.Render(strings.Repeat(" ", screenW))
			continue
		}
		dialogLine := dialogLines[i-top]
		right := max(0, screenW-left-lipgloss.Width(dialogLine))
		lines[i] = bg.Render(strings.Repeat(" ", left)) + dialogLine + bg.Render(strings.Repeat(" ", right))
	}
	return strings.Join(lines, "\n")
}

func (m Model) settingsDialogView(width int, rows int) string {
	help := "Space=toggle  Enter=edit/toggle  s=save  Esc=cancel"
	if m.settingsEditing {
		help = "Enter=apply  Esc=cancel field"
	}
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight(help, width)),
		"",
	}

	items := []struct {
		label  string
		value  string
		isBool bool
	}{
		{label: "Show Git Commands", value: boolSettingValue(m.settingsDraft.ShowGitCommands), isBool: true},
		{label: "Show Repo Info", value: boolSettingValue(m.settingsDraft.ShowRepoInfo), isBool: true},
		{label: "Gerrit Username", value: fallbackValue(m.settingsDraft.GerritUsername, "(unset)")},
		{label: "Gerrit Server", value: fallbackValue(m.settingsDraft.GerritServer, "(unset)")},
		{label: "Base Git Directory", value: fallbackValue(m.settingsDraft.BaseGitDir, "(unset)")},
		{label: "Save Settings", value: "press Enter"},
	}
	for i, item := range items {
		style := m.fgStyle(m.theme.Foreground)
		marker := "  "
		if m.settingsCursor == i {
			marker = "> "
			style = style.Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
		}
		row := fmt.Sprintf("%s%s: %s", marker, item.label, item.value)
		if item.isBool {
			row = fmt.Sprintf("%s%s %s", marker, item.value, item.label)
		}
		lines = append(lines, style.Render(trimRight(row, width)))
	}
	if m.settingsEditing {
		lines = append(lines,
			"",
			m.fgStyle(m.theme.Input).Render(trimRight("Value: "+m.textInput.View(), width)),
		)
	}

	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) gerritDialogView(width int, rows int) string {
	checkedCount := len(m.gerritChecked)
	header := fmt.Sprintf("Space=toggle  /=search  Enter=track checked  a=all  A=none  j/k move  PgUp/PgDn scroll  Esc=close  checked=%d total=%d", checkedCount, len(m.gerritProjects))
	if m.gerritSearching {
		header = "Search Gerrit projects: type to filter, Enter/Esc leave search"
	}
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight(header, width)),
		"",
	}

	if m.gerritLoading {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render(trimRight("(loading projects...)", width)))
		return strings.Join(limitLines(lines, rows), "\n")
	}
	if len(m.gerritProjects) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render(trimRight("(no Gerrit projects loaded)", width)))
		return strings.Join(limitLines(lines, rows), "\n")
	}

	filtered := m.visibleGerritProjects()
	if m.gerritSearching || m.gerritSearchQuery != "" {
		lines = append(lines, m.fgStyle(m.theme.Input).Render(trimRight("/"+m.textInput.View(), width)))
		lines = append(lines, "")
	}
	if len(filtered) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render(trimRight("(no matching Gerrit projects)", width)))
		return strings.Join(limitLines(lines, rows), "\n")
	}

	visibleRows := max(1, rows-3)
	if m.gerritSearching || m.gerritSearchQuery != "" {
		visibleRows = max(1, rows-5)
	}
	start, end := repoViewportRange(makeSequentialIndexes(len(filtered)), m.gerritScroll, visibleRows)
	for i := start; i < end; i++ {
		project := filtered[i]
		marker := "  "
		style := m.fgStyle(m.theme.Foreground)
		if i == m.gerritCursor {
			marker = "> "
			style = style.Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
		}
		box := "[ ]"
		if _, ok := m.gerritChecked[project]; ok {
			box = "[x]"
		}
		lines = append(lines, style.Render(trimRight(marker+box+" "+project, width)))
	}

	indicator := fmt.Sprintf("[%d-%d / %d]", start+1, end, len(filtered))
	lines = append(lines, "", m.fgStyle(m.theme.Muted).Render(trimRight(indicator, width)))
	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) deleteConfirmView(width int, rows int) string {
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight("Enter=remove  Esc=cancel", width)),
		"",
		m.labelValue("Repo", m.deleteConfirmRepo.Name, width),
		m.labelValue("Path", fallbackValue(m.deleteConfirmRepo.Path, "none"), width),
		m.labelValue("Project", fallbackValue(m.deleteConfirmRepo.GerritProject, "none"), width),
		"",
		m.fgStyle(m.theme.Warning).Bold(true).Render(trimRight("This repo is in these favorites lists:", width)),
	}
	for _, name := range m.deleteConfirmLists {
		lines = append(lines, m.labelValue("", name, width))
	}
	lines = append(lines,
		"",
		m.fgStyle(m.theme.Muted).Render(trimRight("Removing will stop tracking the repo and remove it from all favorites lists above.", width)),
	)
	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) favoritesDialogView(width int, rows int) string {
	lists := m.favoriteListNames()
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight("Enter=use  n=new  p=pull  x=delete  Esc=close", width)),
		"",
	}

	if len(lists) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no favorites lists)"))
	} else {
		start := 0
		visibleRows := max(1, rows-5)
		if m.favoritesListCursor >= visibleRows {
			start = m.favoritesListCursor - visibleRows + 1
		}
		end := min(start+visibleRows, len(lists))
		for i := start; i < end; i++ {
			name := lists[i]
			style := m.fgStyle(m.theme.Foreground)
			marker := "  "
			if i == m.favoritesListCursor {
				marker = "> "
				style = style.Foreground(lipgloss.Color(m.theme.Accent)).Bold(true)
			}
			active := " "
			if name == m.activeFavoriteList {
				active = "*"
			}
			count := len(m.favoriteLists[name])
			lines = append(lines, style.Render(trimRight(fmt.Sprintf("%s%s %s (%d)", marker, active, name, count), width)))
		}
	}

	if m.favoritesDialogMode == favoritesDialogCreate {
		lines = append(lines,
			"",
			m.fgStyle(m.theme.Input).Render(trimRight("New list: "+m.textInput.View(), width)),
			m.fgStyle(m.theme.Muted).Render(trimRight("Enter=create  Esc=cancel", width)),
		)
	}

	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) helpView(width int) string {
	raw := []string{
		"Navigation",
		"  j | k           Move Down | Up",
		"  up | down       Move / scroll output",
		"  left | right    Cycle focus",
		"  0               Focus repos",
		"  1               Focus output",
		"  /               Search",
		"  Enter           Maximize Command Output",
		"  PgUp | PgDn     Page output",
		"  space           Toggle select",
		"",
		"Favorites",
		"  f               Toggle filter",
		"  F               Toggle favorite",
		"  l               Open lists",
		"  n               New list",
		"  p               Pull list",
		"  x               Delete list",
		"",
		"Refresh",
		"  r               Refresh repo",
		"  R               Refresh all",
		"",
		"Selection",
		"  a               Select all",
		"  A               Clear all",
		"",
		"Repo Actions",
		"  c               Clone tracked Gerrit repo(s)",
		"  g               Load Gerrit projects",
		"  h               Fetch",
		"  o               Add repo",
		"  s               Scan repos",
		"  p               Pull selected or highlighted",
		"  x               Remove repo",
		"  z               Open lazygit",
		"  v               Open VS Code",
		"  Z               Open Zed",
		"",
		"UI",
		"  +               Toggle repo info",
		"  /               Search",
		"  , | S           Settings",
		"  T               Themes",
		"  ?               Help",
		"  q               Quit",
		"",
		"Settings",
		"  j | k           Move Down | Up",
		"  space           Toggle bool",
		"  Enter           Edit field / toggle / save row",
		"  s               Save",
		"  , | S | Esc     Cancel",
		"",
		"Search",
		"  /               Open",
		"  Enter           Apply",
		"  Esc             Cancel",
		"",
		"Themes",
		"  j | k           Move Down | Up",
		"  Enter           Select",
		"  Esc             Cancel",
		"",
		"Lists",
		"  j | k           Move Down | Up",
		"  Enter           Use list",
		"  n               New list",
		"  p               Pull list",
		"  x               Delete list",
		"  l | Esc         Close",
		"",
		"Press Enter, Esc, or ? to close",
	}
	sections := make([][]string, 0, 12)
	current := make([]string, 0, 8)
	for _, line := range raw {
		if line == "" {
			if len(current) > 0 {
				sections = append(sections, current)
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		sections = append(sections, current)
	}

	if width < 48 || len(sections) < 2 {
		return strings.Join(m.renderHelpSections(sections), "\n")
	}

	leftWidth := max(18, (width-2)/2)
	rightWidth := max(18, width-leftWidth-2)

	leftSections := make([][]string, 0, len(sections))
	rightSections := make([][]string, 0, len(sections))
	leftLinesCount := 0
	rightLinesCount := 0

	for _, section := range sections {
		sectionLines := len(section) + 1
		if leftLinesCount <= rightLinesCount {
			leftSections = append(leftSections, section)
			leftLinesCount += sectionLines
		} else {
			rightSections = append(rightSections, section)
			rightLinesCount += sectionLines
		}
	}

	leftLines := m.renderHelpSections(leftSections)
	rightLines := m.renderHelpSections(rightSections)
	rows := max(len(leftLines), len(rightLines))
	space := m.bgStyle().Render("  ")
	blankLeft := m.bgStyle().Render(strings.Repeat(" ", leftWidth))
	blankRight := m.bgStyle().Render(strings.Repeat(" ", rightWidth))
	lines := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		left := blankLeft
		right := blankRight
		if i < len(leftLines) {
			left = padStyledCell(styledTrimRight(leftLines[i], leftWidth), leftWidth, m.theme.Background)
		}
		if i < len(rightLines) {
			right = padStyledCell(styledTrimRight(rightLines[i], rightWidth), rightWidth, m.theme.Background)
		}
		lines = append(lines, left+space+right)
	}

	return strings.Join(lines, "\n")
}

func (m Model) repoColumnWidths(available int, visible []int) (nameW int, branchW int, authorW int, opW int) {
	minNameW := 12
	minBranchW := 8
	minAuthorW := 8
	minOpW := 6
	if !m.settings.ShowRepoInfo {
		minOpW = 8
	}

	nameW = max(minNameW, lipgloss.Width("Name ("+m.activeFavoriteList+")"))
	branchW = max(minBranchW, lipgloss.Width("Branch"))
	authorW = max(minAuthorW, lipgloss.Width("Author"))
	opW = max(minOpW, lipgloss.Width("Op"))

	for _, idx := range visible {
		repo := m.repos[idx]
		meta := m.repoMetadata(repo)
		nameW = max(nameW, lipgloss.Width(repo.Name))
		branch := meta.CurrentBranch
		if branch == "" {
			branch = "-"
		}
		branchW = max(branchW, lipgloss.Width(branch))
		author := meta.LastCommitAuthor
		if author == "" {
			author = "-"
		}
		authorW = max(authorW, lipgloss.Width(author))
		op := repo.LastOp
		if op == "" {
			op = "-"
		}
		opW = max(opW, lipgloss.Width(op))
	}

	widths := []int{nameW, branchW, authorW, opW}
	mins := []int{minNameW, minBranchW, minAuthorW, minOpW}
	total := nameW + branchW + authorW + opW

	if total > available {
		overflow := total - available
		for overflow > 0 {
			bestIdx := -1
			bestSurplus := 0
			for i := range widths {
				surplus := widths[i] - mins[i]
				if surplus > bestSurplus {
					bestSurplus = surplus
					bestIdx = i
				}
			}
			if bestIdx == -1 {
				break
			}
			widths[bestIdx]--
			overflow--
		}
		total = widths[0] + widths[1] + widths[2] + widths[3]
	}

	if total < available {
		widths[3] += available - total
	}

	return widths[0], widths[1], widths[2], widths[3]
}

func (m Model) renderHelpSections(sections [][]string) []string {
	lines := make([]string, 0, len(sections)*6)
	for sectionIndex, section := range sections {
		if sectionIndex > 0 {
			lines = append(lines, "")
		}
		for _, line := range section {
			switch {
			case !strings.HasPrefix(line, " "):
				lines = append(lines, m.fgStyle(m.theme.Accent).Bold(true).Render(line))
			case strings.HasPrefix(line, "Press "):
				lines = append(lines, m.fgStyle(m.theme.Muted).Render(line))
			default:
				lines = append(lines, m.renderHelpEntryLine(line))
			}
		}
	}
	return lines
}

func (m Model) renderHelpEntryLine(line string) string {
	const keyWidth = 18

	if !strings.Contains(line, " | ") || len(line) <= keyWidth {
		return m.fgStyle(m.theme.Foreground).Render(line)
	}

	keyPart := line[:keyWidth]
	descPart := line[keyWidth:]
	keyStyle := m.fgStyle(m.theme.Foreground)
	sepStyle := m.fgStyle(m.theme.Muted)
	var b strings.Builder

	segments := strings.Split(keyPart, " | ")
	for i, segment := range segments {
		if i > 0 {
			b.WriteString(sepStyle.Render(" | "))
		}
		if segment != "" {
			b.WriteString(keyStyle.Render(segment))
		}
	}
	if descPart != "" {
		b.WriteString(keyStyle.Render(descPart))
	}

	return b.String()
}

func (m Model) labelValue(label string, value string, width int) string {
	labelStyle := m.fgStyle(m.theme.Accent).Bold(true)
	valueStyle := m.fgStyle(m.theme.Foreground)
	prefix := label + ": "
	if label == "" {
		prefix = "  "
	}
	availableWidth := max(1, width-lipgloss.Width(prefix))
	return labelStyle.Render(prefix) + valueStyle.Render(padCell(trimRight(value, availableWidth), availableWidth))
}

func (m *Model) openThemeSelector() {
	if len(m.themeNames) == 0 {
		m.status = "No themes available"
		return
	}
	m.themeSelecting = true
	m.savedTheme = m.theme
	m.savedThemeName = m.themeName
	m.themeCursor = 0
	for i, name := range m.visibleThemeNames() {
		if name == m.themeName {
			m.themeCursor = i
			break
		}
	}
	m.status = "Theme selector: preview with j/k, Enter selects, Esc cancels"
}

func (m *Model) openSettingsDialog() {
	m.settingsDialog = true
	m.settingsCursor = 0
	m.settingsDraft = m.settings
	m.settingsEditing = false
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.status = "Settings: Enter edits, Space toggles, s saves, Esc cancels"
}

func (m Model) handleSettingsDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.settingsEditing {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.settingsEditing = false
			m.textInput.Blur()
			m.textInput.SetValue("")
			m.status = "Canceled settings field edit"
			return m, nil
		case "enter":
			value := strings.TrimSpace(m.textInput.Value())
			switch m.settingsCursor {
			case 2:
				m.settingsDraft.GerritUsername = value
			case 3:
				m.settingsDraft.GerritServer = value
			case 4:
				m.settingsDraft.BaseGitDir = value
			}
			m.settingsEditing = false
			m.textInput.Blur()
			m.textInput.SetValue("")
			m.status = "Updated settings field"
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "S", ",":
		m.settingsDialog = false
		m.settingsDraft = m.settings
		m.settingsEditing = false
		m.status = "Canceled settings"
	case "up", "k":
		m.moveSettingsCursor(-1)
	case "down", "j":
		m.moveSettingsCursor(1)
	case " ":
		m.toggleCurrentSetting()
	case "enter":
		if m.settingsCursor <= 1 {
			m.toggleCurrentSetting()
			return m, nil
		}
		if m.settingsCursor >= 2 && m.settingsCursor <= 4 {
			m.settingsEditing = true
			m.textInput.SetValue(m.currentSettingsFieldValue())
			m.textInput.Focus()
			m.status = "Editing settings field"
			return m, nil
		}
		fallthrough
	case "s":
		m.settings = m.settingsDraft
		m.settingsDialog = false
		m.settingsEditing = false
		m.persist()
		m.status = "Saved settings"
	}
	return m, nil
}

func (m Model) handleGerritDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.gerritSearching {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter", "esc":
			m.gerritSearching = false
			m.textInput.Blur()
			m.status = fmt.Sprintf("Filtered Gerrit projects: %s", m.gerritSearchQuery)
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		m.gerritSearchQuery = strings.TrimSpace(m.textInput.Value())
		m.normalizeGerritCursor()
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "g":
		m.gerritDialog = false
		m.gerritSearching = false
		m.textInput.Blur()
		m.status = "Closed Gerrit projects"
	case "/":
		m.gerritSearching = true
		m.textInput.SetValue(m.gerritSearchQuery)
		m.textInput.Focus()
		m.status = "Search Gerrit projects"
	case "up", "k":
		m.moveGerritCursor(-1)
	case "down", "j":
		m.moveGerritCursor(1)
	case "pgup":
		m.moveGerritCursor(-m.gerritDialogRows())
	case "pgdown":
		m.moveGerritCursor(m.gerritDialogRows())
	case "a":
		m.selectAllGerritProjects()
	case "A":
		m.gerritChecked = map[string]struct{}{}
		m.status = "Cleared Gerrit project selection"
	case " ":
		m.toggleHighlightedGerritProject()
	case "enter":
		added := m.trackCheckedGerritProjects()
		m.gerritDialog = false
		m.normalizeCursor()
		m.ensureRepoCursorVisible(m.repoPanelContentRows())
		m.persist()
		m.status = fmt.Sprintf("Tracked %d Gerrit repositories", added)
		m.logInfo(fmt.Sprintf("gerrit: tracked %d repositories", added))
	}
	return m, nil
}

func (m Model) handleDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.deleteConfirm = false
		m.deleteConfirmRepo = store.Repo{}
		m.deleteConfirmLists = nil
		m.status = "Canceled repo removal"
	case "enter":
		repo := m.deleteConfirmRepo
		m.deleteConfirm = false
		m.deleteConfirmRepo = store.Repo{}
		m.deleteConfirmLists = nil
		m.removeTrackedRepo(repo)
	}
	return m, nil
}

func (m *Model) moveSettingsCursor(delta int) {
	const settingsCount = 6
	m.settingsCursor = (m.settingsCursor + delta + settingsCount) % settingsCount
}

func (m *Model) toggleCurrentSetting() {
	switch m.settingsCursor {
	case 0:
		m.settingsDraft.ShowGitCommands = !m.settingsDraft.ShowGitCommands
		if m.settingsDraft.ShowGitCommands {
			m.status = "Will enable: show git command lines"
		} else {
			m.status = "Will disable: show git command lines"
		}
	case 1:
		m.settingsDraft.ShowRepoInfo = !m.settingsDraft.ShowRepoInfo
		if m.settingsDraft.ShowRepoInfo {
			m.status = "Will enable: show repo info"
		} else {
			m.status = "Will disable: show repo info"
		}
	}
}

func (m Model) currentSettingsFieldValue() string {
	switch m.settingsCursor {
	case 2:
		return m.settingsDraft.GerritUsername
	case 3:
		return m.settingsDraft.GerritServer
	case 4:
		return m.settingsDraft.BaseGitDir
	default:
		return ""
	}
}

func boolSettingValue(value bool) string {
	if value {
		return "[x]"
	}
	return "[ ]"
}

func (m Model) visibleGerritProjects() []string {
	if m.gerritSearchQuery == "" {
		return m.gerritProjects
	}
	filtered := make([]string, 0, len(m.gerritProjects))
	for _, project := range m.gerritProjects {
		if containsFold(project, m.gerritSearchQuery) {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func (m *Model) normalizeGerritCursor() {
	filtered := m.visibleGerritProjects()
	if len(filtered) == 0 {
		m.gerritCursor = 0
		m.gerritScroll = 0
		return
	}
	if m.gerritCursor < 0 {
		m.gerritCursor = 0
	}
	if m.gerritCursor >= len(filtered) {
		m.gerritCursor = len(filtered) - 1
	}
	m.ensureGerritCursorVisible()
}

func (m *Model) moveGerritCursor(delta int) {
	filtered := m.visibleGerritProjects()
	if len(filtered) == 0 {
		m.gerritCursor = 0
		m.gerritScroll = 0
		return
	}
	m.gerritCursor += delta
	if m.gerritCursor < 0 {
		m.gerritCursor = 0
	}
	if m.gerritCursor >= len(filtered) {
		m.gerritCursor = len(filtered) - 1
	}
	m.ensureGerritCursorVisible()
}

func (m *Model) ensureGerritCursorVisible() {
	filtered := m.visibleGerritProjects()
	rows := m.gerritDialogRows()
	if rows <= 0 {
		rows = 1
	}
	if len(filtered) == 0 {
		m.gerritCursor = 0
		m.gerritScroll = 0
		return
	}
	if m.gerritScroll < 0 {
		m.gerritScroll = 0
	}
	if m.gerritCursor < m.gerritScroll {
		m.gerritScroll = m.gerritCursor
	}
	if m.gerritCursor >= m.gerritScroll+rows {
		m.gerritScroll = m.gerritCursor - rows + 1
	}
	maxScroll := max(0, len(filtered)-rows)
	if m.gerritScroll > maxScroll {
		m.gerritScroll = maxScroll
	}
}

func (m Model) gerritDialogRows() int {
	screenH := max(1, m.height-2)
	dialogH := min(max(18, screenH-2), max(18, screenH))
	return max(1, dialogH-5)
}

func (m *Model) toggleHighlightedGerritProject() {
	filtered := m.visibleGerritProjects()
	if m.gerritCursor < 0 || m.gerritCursor >= len(filtered) {
		return
	}
	project := filtered[m.gerritCursor]
	if m.gerritChecked == nil {
		m.gerritChecked = map[string]struct{}{}
	}
	if _, ok := m.gerritChecked[project]; ok {
		delete(m.gerritChecked, project)
		m.status = "Unchecked Gerrit project"
		return
	}
	m.gerritChecked[project] = struct{}{}
	m.status = "Checked Gerrit project"
}

func (m *Model) selectAllGerritProjects() {
	if m.gerritChecked == nil {
		m.gerritChecked = map[string]struct{}{}
	}
	for _, project := range m.visibleGerritProjects() {
		m.gerritChecked[project] = struct{}{}
	}
	m.status = fmt.Sprintf("Selected all %d visible Gerrit projects", len(m.visibleGerritProjects()))
}

func (m *Model) trackCheckedGerritProjects() int {
	if len(m.gerritChecked) == 0 {
		return 0
	}
	projects := make([]string, 0, len(m.gerritChecked))
	for project := range m.gerritChecked {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	added := 0
	for _, project := range projects {
		if m.trackGerritProject(project) {
			added++
		}
	}
	return added
}

func (m *Model) toggleShowRepoInfo() {
	m.settings.ShowRepoInfo = !m.settings.ShowRepoInfo
	m.persist()
	if m.settings.ShowRepoInfo {
		m.status = "Enabled: show repo info"
	} else {
		m.status = "Disabled: show repo info"
	}
}

func (m *Model) toggleOutputMaximized() {
	m.outputMaximized = !m.outputMaximized
	if m.outputMaximized {
		m.focus = focusOutput
		m.status = "Maximized command output"
		return
	}
	m.status = "Normal command output"
}

func (m *Model) closeSearchMode(cancel bool) {
	if cancel {
		m.setActiveSearchQuery(m.searchBackupQuery)
		m.status = "Canceled search"
	} else {
		m.status = m.searchStatus(m.activeSearchQuery())
	}
	m.inputMode = inputNone
	m.textInput.Blur()
}

func (m Model) handleThemeSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.theme = m.savedTheme
		m.themeName = m.savedThemeName
		m.themeSelecting = false
		m.status = "Theme selection canceled"
	case "enter":
		m.themeSelecting = false
		if err := saveActiveTheme(m.themeName); err != nil {
			m.status = fmt.Sprintf("Theme selected, save failed: %v", err)
			m.logError("theme: save failed: " + err.Error())
			return m, nil
		}
		m.savedTheme = m.theme
		m.savedThemeName = m.themeName
		m.status = fmt.Sprintf("Theme selected: %s", m.themeName)
		m.logInfo("theme selected: " + m.themeName)
	case "up", "k":
		m.moveThemeCursor(-1)
	case "down", "j":
		m.moveThemeCursor(1)
	}
	return m, nil
}

func (m *Model) moveThemeCursor(delta int) {
	visible := m.visibleThemeNames()
	if len(visible) == 0 {
		return
	}
	m.themeCursor = (m.themeCursor + delta + len(visible)) % len(visible)
	name := visible[m.themeCursor]
	if palette, ok := m.themes[name]; ok {
		m.theme = palette
		m.themeName = name
		m.status = "Previewing theme: " + name
	}
}

func limitLines(lines []string, maxLines int) []string {
	if maxLines < 0 {
		maxLines = 0
	}
	if len(lines) <= maxLines {
		return lines
	}
	return lines[:maxLines]
}

func favoriteListsFromState(lists map[string][]string) map[string]map[string]struct{} {
	normalized := make(map[string]map[string]struct{})
	if len(lists) == 0 {
		normalized[defaultFavoriteListName] = map[string]struct{}{}
		return normalized
	}
	for name, paths := range lists {
		if name == "" {
			continue
		}
		set := make(map[string]struct{}, len(paths))
		for _, path := range paths {
			if path == "" {
				continue
			}
			set[path] = struct{}{}
		}
		normalized[name] = set
	}
	if len(normalized) == 0 {
		normalized[defaultFavoriteListName] = map[string]struct{}{}
	}
	return normalized
}

func (m Model) favoriteListsForStore() map[string][]string {
	lists := make(map[string][]string, len(m.favoriteLists))
	for name, members := range m.favoriteLists {
		paths := make([]string, 0, len(members))
		for path := range members {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		lists[name] = paths
	}
	if len(lists) == 0 {
		lists[defaultFavoriteListName] = []string{}
	}
	return lists
}

func (m Model) favoriteListNames() []string {
	names := make([]string, 0, len(m.favoriteLists))
	for name := range m.favoriteLists {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Model) currentFavoriteSet() map[string]struct{} {
	if m.favoriteLists == nil {
		m.favoriteLists = map[string]map[string]struct{}{}
	}
	set, ok := m.favoriteLists[m.activeFavoriteList]
	if !ok {
		set = map[string]struct{}{}
		m.favoriteLists[m.activeFavoriteList] = set
	}
	return set
}

func (m Model) isFavorite(path string) bool {
	set := m.favoriteLists[m.activeFavoriteList]
	if set == nil {
		return false
	}
	_, ok := set[path]
	return ok
}

func (m *Model) toggleFavorite(path string) bool {
	set := m.currentFavoriteSet()
	if _, ok := set[path]; ok {
		delete(set, path)
		return false
	}
	set[path] = struct{}{}
	return true
}

func (m *Model) removeRepoFromFavorites(path string) {
	for _, set := range m.favoriteLists {
		delete(set, path)
	}
}

func (m Model) favoriteListsContaining(path string) []string {
	names := make([]string, 0, len(m.favoriteLists))
	for name, set := range m.favoriteLists {
		if _, ok := set[path]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (m *Model) removeTrackedRepo(repo store.Repo) {
	key := m.repoKey(repo)
	for i := range m.repos {
		if m.repoKey(m.repos[i]) != key {
			continue
		}
		m.repos = append(m.repos[:i], m.repos[i+1:]...)
		break
	}
	m.removeRepoFromFavorites(key)
	delete(m.repoMeta, key)
	m.normalizeCursor()
	m.ensureRepoCursorVisible(m.repoPanelContentRows())
	m.persist()
	m.status = fmt.Sprintf("Removed: %s", repo.Name)
	m.logInfo(fmt.Sprintf("removed: %s (%s)", repo.Name, fallbackValue(repo.Path, repo.GerritProject)))
}

func (m Model) visibleRepoIndexes() []int {
	indexes := make([]int, 0, len(m.repos))
	for i, repo := range m.repos {
		if m.favoritesOnly && !m.isFavorite(m.repoKey(repo)) {
			continue
		}
		if !m.repoMatchesSearch(repo) {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func (m Model) currentRepoIndex() (int, bool) {
	if len(m.repos) == 0 {
		return 0, false
	}
	if !m.favoritesOnly {
		if m.cursor < 0 || m.cursor >= len(m.repos) {
			return 0, false
		}
		return m.cursor, true
	}
	for _, idx := range m.visibleRepoIndexes() {
		if idx == m.cursor {
			return idx, true
		}
	}
	return 0, false
}

func (m *Model) normalizeCursor() {
	if len(m.repos) == 0 {
		m.cursor = 0
		m.repoScroll = 0
		return
	}
	if !m.favoritesOnly {
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor >= len(m.repos) {
			m.cursor = len(m.repos) - 1
		}
		m.ensureRepoCursorVisible(m.repoPanelContentRows())
		return
	}
	visible := m.visibleRepoIndexes()
	if len(visible) == 0 {
		if m.cursor >= len(m.repos) {
			m.cursor = len(m.repos) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.ensureRepoCursorVisible(m.repoPanelContentRows())
		return
	}
	for _, idx := range visible {
		if idx == m.cursor {
			return
		}
	}
	m.cursor = visible[0]
	m.ensureRepoCursorVisible(m.repoPanelContentRows())
}

func (m *Model) moveRepoCursor(delta int) {
	visible := m.visibleRepoIndexes()
	if len(visible) == 0 {
		return
	}
	position := 0
	found := false
	for i, idx := range visible {
		if idx == m.cursor {
			position = i
			found = true
			break
		}
	}
	if !found {
		m.cursor = visible[0]
		return
	}
	next := position + delta
	if next < 0 {
		next = 0
	}
	if next >= len(visible) {
		next = len(visible) - 1
	}
	m.cursor = visible[next]
	m.ensureRepoCursorVisible(m.repoPanelContentRows())
}

func repoViewportRange(visible []int, scroll int, rows int) (start int, end int) {
	if len(visible) == 0 {
		return 0, 0
	}
	if rows <= 0 || rows >= len(visible) {
		return 0, len(visible)
	}
	start = scroll
	if start < 0 {
		start = 0
	}
	maxStart := max(0, len(visible)-rows)
	if start > maxStart {
		start = maxStart
	}
	end = min(len(visible), start+rows)
	return start, end
}

func (m *Model) ensureRepoCursorVisible(rows int) {
	visible := m.visibleRepoIndexes()
	if len(visible) == 0 {
		m.repoScroll = 0
		return
	}
	if rows <= 0 {
		rows = 1
	}

	cursorPos := 0
	found := false
	for i, idx := range visible {
		if idx == m.cursor {
			cursorPos = i
			found = true
			break
		}
	}
	if !found {
		m.repoScroll = 0
		return
	}

	if m.repoScroll < 0 {
		m.repoScroll = 0
	}
	if cursorPos < m.repoScroll {
		m.repoScroll = cursorPos
	}
	if cursorPos >= m.repoScroll+rows {
		m.repoScroll = cursorPos - rows + 1
	}

	maxScroll := max(0, len(visible)-rows)
	if m.repoScroll > maxScroll {
		m.repoScroll = maxScroll
	}
}

func (m *Model) openFavoritesDialog() {
	if len(m.favoriteLists) == 0 {
		m.favoriteLists = map[string]map[string]struct{}{
			defaultFavoriteListName: {},
		}
	}
	m.favoritesDialog = true
	m.favoritesDialogMode = favoritesDialogSelect
	m.textInput.Blur()
	m.textInput.SetValue("")
	m.favoritesListCursor = 0
	for i, name := range m.favoriteListNames() {
		if name == m.activeFavoriteList {
			m.favoritesListCursor = i
			break
		}
	}
	m.status = "Favorites lists: Enter selects, n creates, p pulls, x deletes, Esc closes"
}

func (m Model) handleFavoritesDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.favoritesDialogMode == favoritesDialogCreate {
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.favoritesDialogMode = favoritesDialogSelect
			m.textInput.Blur()
			m.textInput.SetValue("")
			m.status = "Favorites list creation canceled"
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.textInput.Value())
			m.textInput.Blur()
			m.textInput.SetValue("")
			m.favoritesDialogMode = favoritesDialogSelect
			if name == "" {
				m.status = "Favorites list name required"
				return m, nil
			}
			if _, ok := m.favoriteLists[name]; ok {
				m.status = fmt.Sprintf("Favorites list already exists: %s", name)
				return m, nil
			}
			m.favoriteLists[name] = map[string]struct{}{}
			m.activeFavoriteList = name
			m.persist()
			m.openFavoritesDialog()
			m.status = fmt.Sprintf("Created favorites list: %s", name)
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "l":
		m.favoritesDialog = false
		m.status = "Closed favorites lists"
	case "up", "k":
		m.moveFavoritesListCursor(-1)
	case "down", "j":
		m.moveFavoritesListCursor(1)
	case "enter":
		if name, ok := m.highlightedFavoriteListName(); ok {
			m.activeFavoriteList = name
			m.normalizeCursor()
			m.persist()
			m.favoritesDialog = false
			m.status = fmt.Sprintf("Using favorites list: %s", name)
		}
	case "n":
		m.favoritesDialogMode = favoritesDialogCreate
		m.textInput.SetValue("")
		m.textInput.Focus()
		m.status = "Create favorites list: enter name"
	case "p":
		if m.busy {
			m.status = "Busy running pull"
			return m, nil
		}
		name, ok := m.highlightedFavoriteListName()
		if !ok {
			m.status = "No favorites list highlighted"
			return m, nil
		}
		repos := selectableRepos(m.favoriteRepos(name))
		if len(repos) == 0 {
			m.status = fmt.Sprintf("No cloned repositories in favorites list: %s", name)
			m.logInfo("Pull: no cloned repositories in favorites list: " + name)
			return m, nil
		}
		m.busy = true
		m.status = fmt.Sprintf("Pulling %d repositories from favorites list %s...", len(repos), name)
		m.logPullStart("favorites list "+name, repos)
		m.scrollToBottom(m.outPanelHeight())
		return m, pullSelectedCmd(repos)
	case "x":
		if name, ok := m.highlightedFavoriteListName(); ok {
			if len(m.favoriteLists) == 1 {
				m.status = "Cannot delete the only favorites list"
				return m, nil
			}
			delete(m.favoriteLists, name)
			if m.activeFavoriteList == name {
				m.activeFavoriteList = m.favoriteListNames()[0]
			}
			m.normalizeCursor()
			m.persist()
			if m.favoritesListCursor >= len(m.favoriteLists) {
				m.favoritesListCursor = max(0, len(m.favoriteLists)-1)
			}
			m.status = fmt.Sprintf("Deleted favorites list: %s", name)
		}
	}
	return m, nil
}

func (m *Model) moveFavoritesListCursor(delta int) {
	names := m.favoriteListNames()
	if len(names) == 0 {
		m.favoritesListCursor = 0
		return
	}
	m.favoritesListCursor = (m.favoritesListCursor + delta + len(names)) % len(names)
}

func (m Model) highlightedFavoriteListName() (string, bool) {
	names := m.favoriteListNames()
	if len(names) == 0 {
		return "", false
	}
	if m.favoritesListCursor < 0 {
		return names[0], true
	}
	if m.favoritesListCursor >= len(names) {
		return names[len(names)-1], true
	}
	return names[m.favoritesListCursor], true
}

func (m Model) favoriteRepos(listName string) []store.Repo {
	paths := m.favoriteLists[listName]
	if len(paths) == 0 {
		return nil
	}
	repos := make([]store.Repo, 0, len(paths))
	for _, repo := range m.repos {
		if _, ok := paths[m.repoKey(repo)]; ok {
			repos = append(repos, repo)
		}
	}
	return repos
}

func (m Model) handleInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inputMode == inputSearch {
		switch msg.String() {
		case "esc":
			m.closeSearchMode(true)
			return m, nil
		case "enter":
			m.closeSearchMode(false)
			return m, nil
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		m.setActiveSearchQuery(strings.TrimSpace(m.textInput.Value()))
		m.status = m.searchStatus(m.textInput.Value())
		return m, cmd
	}

	switch msg.String() {
	case "esc":
		m.inputMode = inputNone
		m.textInput.Blur()
		m.status = "Canceled"
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.textInput.Value())
		m.textInput.Blur()
		if value == "" {
			m.inputMode = inputNone
			m.status = "No input provided"
			return m, nil
		}

		if m.inputMode == inputAddOne {
			added, err := m.addRepo(value)
			m.inputMode = inputNone
			if err != nil {
				m.status = err.Error()
				m.logError("add: " + err.Error())
				return m, nil
			}
			if !added {
				m.status = "Repository already tracked"
				m.logInfo("add: already tracked: " + value)
				return m, nil
			}
			m.status = "Repository added"
			m.logSuccess("add: " + value)
			m.scrollToBottom(m.outPanelHeight())
			return m, nil
		}

		if m.inputMode == inputScan {
			root, err := expandPath(value)
			if err != nil {
				m.inputMode = inputNone
				m.status = fmt.Sprintf("Invalid path: %v", err)
				m.logError("scan: invalid path: " + err.Error())
				return m, nil
			}
			repos, err := discovery.ScanGitRepos(root)
			if err != nil {
				m.inputMode = inputNone
				m.status = fmt.Sprintf("Scan failed: %v", err)
				m.logError("scan: " + err.Error())
				return m, nil
			}
			addedCount := 0
			for _, path := range repos {
				added, addErr := m.addRepo(path)
				if addErr == nil && added {
					addedCount++
					m.logSuccess("scan added: " + path)
				}
			}
			m.inputMode = inputNone
			summary := fmt.Sprintf("Scan complete: %d new repos added", addedCount)
			m.status = summary
			m.logInfo("--- " + summary + " ---")
			m.scrollToBottom(m.outPanelHeight())
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m *Model) addRepo(rawPath string) (bool, error) {
	path, err := expandPath(rawPath)
	if err != nil {
		return false, err
	}

	if !gitutil.IsGitRepo(path) {
		return false, fmt.Errorf("not a git repo: %s", path)
	}

	for _, r := range m.repos {
		if r.Path == path || m.repoKey(r) == path {
			return false, nil
		}
	}
	for i := range m.repos {
		expectedPath := m.expectedRepoPath(m.repos[i])
		if expectedPath == "" || expectedPath != path {
			continue
		}
		m.repos[i].Path = path
		if m.repos[i].Name == "" {
			m.repos[i].Name = filepath.Base(path)
		}
		m.refreshRepoStatus(m.repos[i])
		m.normalizeCursor()
		m.persist()
		return false, nil
	}

	m.repos = append(m.repos, store.Repo{
		Name: filepath.Base(path),
		Path: path,
	})
	sort.Slice(m.repos, func(i, j int) bool {
		return strings.ToLower(m.repos[i].Name) < strings.ToLower(m.repos[j].Name)
	})

	if len(m.repos) == 1 {
		m.cursor = 0
	}
	m.refreshRepoStatus(m.repos[len(m.repos)-1])
	m.normalizeCursor()
	m.persist()
	return true, nil
}

func (m *Model) trackGerritProject(project string) bool {
	project = m.normalizeGerritProject(project)
	if project == "" {
		return false
	}
	cfg := m.gerritConfig()
	baseDir := cfg.BaseDir
	if expandedBaseDir, err := expandPath(baseDir); err == nil {
		baseDir = expandedBaseDir
	}
	path := ""
	remoteURL := ""
	if strings.TrimSpace(baseDir) != "" {
		path = gerrit.LocalPath(baseDir, project)
	}
	if strings.TrimSpace(cfg.Target()) != "" {
		remoteURL = gerrit.BuildCloneURL(cfg.Target(), project)
	}

	for i := range m.repos {
		if path != "" && m.repos[i].Path == path {
			if m.repos[i].GerritProject == "" {
				m.repos[i].GerritProject = project
			}
			m.repos[i].RemoteURL = remoteURL
			if m.repos[i].Name == "" {
				m.repos[i].Name = filepath.Base(project)
			}
			m.refreshRepoStatus(m.repos[i])
			return false
		}
		if m.repos[i].GerritProject == project {
			if m.repos[i].Path == "" || !gitutil.IsGitRepo(m.repos[i].Path) {
				m.repos[i].Path = path
			}
			m.repos[i].RemoteURL = remoteURL
			if m.repos[i].Name == "" {
				m.repos[i].Name = filepath.Base(project)
			}
			m.refreshRepoStatus(m.repos[i])
			return false
		}
	}

	repo := store.Repo{
		Name:          filepath.Base(project),
		Path:          path,
		GerritProject: project,
		RemoteURL:     remoteURL,
	}
	m.repos = append(m.repos, repo)
	sort.Slice(m.repos, func(i, j int) bool {
		return strings.ToLower(m.repos[i].Name) < strings.ToLower(m.repos[j].Name)
	})
	if len(m.repos) == 1 {
		m.cursor = 0
	}
	m.refreshRepoStatuses()
	return true
}

func (m Model) gerritConfig() gerrit.Config {
	return gerrit.Config{
		Username: strings.TrimSpace(m.settings.GerritUsername),
		Server:   strings.TrimSpace(m.settings.GerritServer),
		BaseDir:  strings.TrimSpace(m.settings.BaseGitDir),
	}
}

func (m Model) expectedRepoPath(repo store.Repo) string {
	if strings.TrimSpace(repo.GerritProject) == "" {
		return ""
	}
	cfg := m.gerritConfig()
	baseDir := cfg.BaseDir
	if expandedBaseDir, err := expandPath(baseDir); err == nil {
		baseDir = expandedBaseDir
	}
	if strings.TrimSpace(baseDir) == "" {
		return ""
	}
	return gerrit.LocalPath(baseDir, repo.GerritProject)
}

func (m Model) normalizeGerritProject(project string) string {
	project = strings.Trim(strings.TrimSpace(project), "/")
	if project == "" {
		return ""
	}

	cfg := m.gerritConfig()
	baseDir := cfg.BaseDir
	if expandedBaseDir, err := expandPath(baseDir); err == nil {
		baseDir = expandedBaseDir
	}

	if strings.HasPrefix(project, "~") || strings.HasPrefix(project, "/") {
		if expandedProject, err := expandPath(project); err == nil && strings.TrimSpace(baseDir) != "" {
			if rel, err := filepath.Rel(baseDir, expandedProject); err == nil {
				rel = filepath.ToSlash(rel)
				if rel != "." && !strings.HasPrefix(rel, "../") && rel != ".." {
					return strings.Trim(rel, "/")
				}
			}
		}
	}

	return project
}

func (m Model) isCloneableRepo(repo store.Repo) bool {
	if strings.TrimSpace(repo.Path) == "" || strings.TrimSpace(repo.RemoteURL) == "" {
		return false
	}
	return !gitutil.IsGitRepo(repo.Path)
}

func (m *Model) persist() {
	if m.store == nil {
		return
	}
	state := store.State{
		Repos:              m.repos,
		FavoriteLists:      m.favoriteListsForStore(),
		ActiveFavoriteList: m.activeFavoriteList,
		Settings:           m.settings,
	}
	if err := m.store.Save(state); err != nil {
		m.status = fmt.Sprintf("Save error: %v", err)
	}
}

func selectedRepos(repos []store.Repo) []store.Repo {
	selected := make([]store.Repo, 0, len(repos))
	for _, r := range repos {
		if r.Selected {
			selected = append(selected, r)
		}
	}
	return selected
}

func selectableRepos(repos []store.Repo) []store.Repo {
	selected := make([]store.Repo, 0, len(repos))
	for _, r := range repos {
		if strings.TrimSpace(r.Path) == "" {
			continue
		}
		selected = append(selected, r)
	}
	return selected
}

func cloneableRepos(repos []store.Repo) []store.Repo {
	selected := make([]store.Repo, 0, len(repos))
	for _, r := range repos {
		if strings.TrimSpace(r.Path) == "" || strings.TrimSpace(r.RemoteURL) == "" {
			continue
		}
		if gitutil.IsGitRepo(r.Path) {
			continue
		}
		selected = append(selected, r)
	}
	return selected
}

func makeSequentialIndexes(count int) []int {
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

func (m *Model) logPullStart(scope string, repos []store.Repo) {
	m.logInfo(fmt.Sprintf("--- Pull started: %s (%d repos) ---", scope, len(repos)))
	for _, r := range repos {
		m.logInfo(fmt.Sprintf("  queued: %s", r.Name))
		if m.settings.ShowGitCommands && strings.TrimSpace(r.Path) != "" {
			m.logInfo("  $ " + gitutil.PullCommand(r.Path))
		}
	}
}

func (m *Model) logFetchStart(scope string, repos []store.Repo) {
	m.logInfo(fmt.Sprintf("--- Fetch started: %s (%d repos) ---", scope, len(repos)))
	for _, r := range repos {
		m.logInfo(fmt.Sprintf("  queued: %s", r.Name))
		if m.settings.ShowGitCommands && strings.TrimSpace(r.Path) != "" {
			m.logInfo("  $ " + gitutil.FetchCommand(r.Path))
		}
	}
}

func (m *Model) logCloneStart(scope string, repos []store.Repo) {
	m.logInfo(fmt.Sprintf("--- Clone started: %s (%d repos) ---", scope, len(repos)))
	for _, r := range repos {
		m.logInfo(fmt.Sprintf("  queued: %s", r.Name))
		if m.settings.ShowGitCommands && strings.TrimSpace(r.Path) != "" && strings.TrimSpace(r.RemoteURL) != "" {
			m.logInfo("  $ " + gitutil.CloneCommand(r.RemoteURL, r.Path))
		}
	}
}

func pullSelectedCmd(repos []store.Repo) tea.Cmd {
	return func() tea.Msg {
		const maxWorkers = 4
		sem := make(chan struct{}, maxWorkers)
		results := make([]pullResult, len(repos))
		var wg sync.WaitGroup

		for i := range repos {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				out, err := gitutil.Pull(repos[i].Path)
				results[i] = pullResult{
					path:   store.RepoKey(repos[i]),
					output: out,
					err:    err,
				}
			}(i)
		}

		wg.Wait()
		return pullFinishedMsg{results: results}
	}
}

func fetchSelectedCmd(repos []store.Repo) tea.Cmd {
	return func() tea.Msg {
		const maxWorkers = 4
		sem := make(chan struct{}, maxWorkers)
		results := make([]pullResult, len(repos))
		var wg sync.WaitGroup

		for i := range repos {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				out, err := gitutil.Fetch(repos[i].Path)
				results[i] = pullResult{
					path:   store.RepoKey(repos[i]),
					output: out,
					err:    err,
				}
			}(i)
		}

		wg.Wait()
		return fetchFinishedMsg{results: results}
	}
}

func cloneSelectedCmd(repos []store.Repo) tea.Cmd {
	return func() tea.Msg {
		const maxWorkers = 4
		sem := make(chan struct{}, maxWorkers)
		results := make([]pullResult, len(repos))
		var wg sync.WaitGroup

		for i := range repos {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				out, err := gitutil.Clone(repos[i].RemoteURL, repos[i].Path)
				results[i] = pullResult{
					path:   store.RepoKey(repos[i]),
					output: out,
					err:    err,
				}
			}(i)
		}

		wg.Wait()
		return cloneFinishedMsg{results: results}
	}
}

func loadGerritProjectsCmd(cfg gerrit.Config) tea.Cmd {
	return func() tea.Msg {
		projects, output, err := gerrit.ListProjects(cfg)
		return gerritProjectsLoadedMsg{
			projects: projects,
			output:   output,
			err:      err,
		}
	}
}

func openEditorCmd(editor string, executable string, repo store.Repo) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command(executable, repo.Path)
		err := cmd.Start()
		if err == nil {
			err = cmd.Process.Release()
		}
		return vscodeOpenedMsg{
			editor:   editor,
			repoName: repo.Name,
			path:     store.RepoKey(repo),
			err:      err,
		}
	}
}

func expandPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}

	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}

	if runtime.GOOS == "windows" {
		p = strings.Trim(p, "\"")
	}
	return p, nil
}

func trimRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 3 {
		return string(r[:width])
	}
	return string(r[:width-3]) + "..."
}

func padCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = trimRight(s, width)
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

func padStyledCell(s string, width int, bg string) string {
	if width <= 0 {
		return ""
	}
	padding := width - lipgloss.Width(s)
	if padding <= 0 {
		return s
	}
	return s + lipgloss.NewStyle().Background(lipgloss.Color(bg)).Render(strings.Repeat(" ", padding))
}

func (m Model) indentBody(body string, width int) string {
	if width <= 0 {
		return ""
	}
	const indent = 1
	lines := strings.Split(body, "\n")
	paddingWidth := min(indent, width)
	prefix := m.bgStyle().Render(strings.Repeat(" ", paddingWidth))
	for i, line := range lines {
		if width <= paddingWidth {
			lines[i] = prefix
			continue
		}
		lines[i] = prefix + styledTrimRight(line, width-paddingWidth)
	}
	if len(lines) == 0 {
		return m.bgStyle().Render(strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

func styledTrimRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return truncate.String(s, uint(width))
	}
	return truncate.StringWithTail(s, uint(width), "...")
}

func formatLastUpdated(value string) string {
	if strings.TrimSpace(value) == "" {
		return "never"
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return ts.Local().Format("2006-01-02 15:04")
}

func formatLastUpdatedShort(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "-"
	}
	return formatRelativeAgeShort(ts)
}

func formatCommitAge(value time.Time) string {
	if value.IsZero() {
		return "none"
	}
	return formatRelativeAgeLong(value)
}

func formatCommitAgeShort(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return formatRelativeAgeShort(value)
}

func formatRelativeAgeShort(ts time.Time) string {
	d := time.Since(ts)
	if d < 0 {
		d = 0
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		if minutes < 1 {
			minutes = 1
		}
		return fmt.Sprintf("%dm", minutes)
	}
	if d < 24*time.Hour {
		hours := int(d / time.Hour)
		return fmt.Sprintf("%dh", hours)
	}
	days := int(d / (24 * time.Hour))
	return fmt.Sprintf("%dd", days)
}

func formatRelativeAgeLong(ts time.Time) string {
	d := time.Since(ts)
	if d < 0 {
		d = 0
	}
	if d < time.Hour {
		minutes := int(d / time.Minute)
		if minutes < 1 {
			minutes = 1
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	}
	if d < 24*time.Hour {
		hours := int(d / time.Hour)
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	days := int(d / (24 * time.Hour))
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func fallbackValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func formatRemoteBranchCount(count int) string {
	if count == 0 {
		return "0"
	}
	if count == 1 {
		return "1 branch"
	}
	return fmt.Sprintf("%d branches", count)
}

func formatOpWithRemoteBranchDelta(base string, newCount int) string {
	if newCount <= 0 {
		return base
	}
	if newCount == 1 {
		return base + " (+1 remote branch)"
	}
	return fmt.Sprintf("%s (+%d remote branches)", base, newCount)
}

func diffBranches(current []string, previous []string) []string {
	if len(current) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(previous))
	for _, branch := range previous {
		seen[branch] = struct{}{}
	}
	newBranches := make([]string, 0, len(current))
	for _, branch := range current {
		if _, ok := seen[branch]; ok {
			continue
		}
		newBranches = append(newBranches, branch)
	}
	return newBranches
}

func formatSyncCounts(meta gitutil.RepoMetadata) string {
	if !meta.HasUpstream {
		return "-"
	}
	if meta.AheadCount == 0 && meta.BehindCount == 0 {
		return "✓"
	}
	if meta.AheadCount > 0 && meta.BehindCount > 0 {
		return fmt.Sprintf("↑%d↓%d", meta.AheadCount, meta.BehindCount)
	}
	if meta.AheadCount > 0 {
		return fmt.Sprintf("↑%d", meta.AheadCount)
	}
	return fmt.Sprintf("↓%d", meta.BehindCount)
}

func (m Model) renderSyncCell(meta gitutil.RepoMetadata, rowBg string, width int) string {
	if !meta.HasUpstream {
		return m.fgBgStyle(m.theme.Muted, rowBg).Render(padCell("-", width))
	}
	if meta.AheadCount == 0 && meta.BehindCount == 0 {
		return m.fgBgStyle(m.theme.Success, rowBg).Bold(true).Render(padCell("✓", width))
	}

	content := ""
	if meta.AheadCount > 0 {
		content += m.fgBgStyle(m.theme.Success, rowBg).Bold(true).Render(fmt.Sprintf("↑%d", meta.AheadCount))
	}
	if meta.BehindCount > 0 {
		content += m.fgBgStyle(m.theme.Error, rowBg).Bold(true).Render(fmt.Sprintf("↓%d", meta.BehindCount))
	}
	return padStyledCell(content, width, rowBg)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
