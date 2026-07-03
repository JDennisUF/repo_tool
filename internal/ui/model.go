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
	"repo_tool/internal/gitutil"
	"repo_tool/internal/store"
)

type inputMode int

const (
	inputNone inputMode = iota
	inputAddOne
	inputScan
)

const defaultFavoriteListName = "default"

type focusSection int

const (
	focusRepos focusSection = iota
	focusInfo
	focusOutput
)

const focusSectionCount = int(focusOutput) + 1

type pullResult struct {
	path   string
	output string
	err    error
}

type pullFinishedMsg struct {
	results []pullResult
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
	repos    []store.Repo
	cursor   int
	width    int
	height   int
	status   string
	busy     bool
	showHelp bool
	focus    focusSection

	// output panel
	output    []outputLine
	outScroll int // index of first visible line

	store               *store.Store
	textInput           textinput.Model
	inputMode           inputMode
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
	repoMeta            map[string]gitutil.RepoMetadata
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
	case focusInfo:
		m.status = "Focused [1] Repo Info"
	case focusOutput:
		m.status = "Focused [2] Command Output"
	}
}

func (m *Model) cycleFocus(delta int) {
	next := (int(m.focus) + delta + focusSectionCount) % focusSectionCount
	m.setFocus(focusSection(next))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.Width = max(20, msg.Width/2-10)
		return m, nil

	case pullFinishedMsg:
		m.busy = false
		successes := 0
		failures := 0
		for i := range m.repos {
			for _, r := range msg.results {
				if m.repos[i].Path != r.path {
					continue
				}
				if r.err != nil {
					m.repos[i].LastOp = "pull failed"
					failures++
					m.logError(fmt.Sprintf("[%s] FAIL: %s", m.repos[i].Name, r.output))
				} else {
					m.repos[i].LastOp = "pull ok"
					m.repos[i].LastUpdated = time.Now().Format(time.RFC3339)
					successes++
					m.logSuccess(fmt.Sprintf("[%s] OK: %s", m.repos[i].Name, r.output))
				}
			}
		}
		m.persist()
		m.refreshRepoStatuses()
		summary := fmt.Sprintf("Pull complete: %d ok, %d failed", successes, failures)
		m.status = summary
		m.logInfo("--- " + summary + " ---")
		m.scrollToBottom(m.outPanelHeight())
		return m, nil

	case lazygitExitedMsg:
		if msg.path != "" {
			m.refreshRepoStatus(msg.path)
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
		if m.favoritesDialog {
			return m.handleFavoritesDialog(msg)
		}
		if m.inputMode != inputNone {
			return m.handleInputMode(msg)
		}
		if m.themeSelecting {
			return m.handleThemeSelector(msg)
		}
		if m.showHelp {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "?", "esc", "enter", "q":
				m.showHelp = false
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "0":
			m.setFocus(focusRepos)
		case "1":
			m.setFocus(focusInfo)
		case "2":
			m.setFocus(focusOutput)
		case "left":
			m.cycleFocus(-1)
		case "right":
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
		case " ":
			if idx, ok := m.currentRepoIndex(); ok {
				m.repos[idx].Selected = !m.repos[idx].Selected
				m.persist()
			}
		case "f":
			m.favoritesOnly = !m.favoritesOnly
			m.normalizeCursor()
			if m.favoritesOnly {
				m.status = fmt.Sprintf("Favorites filter enabled: %s", m.activeFavoriteList)
			} else {
				m.status = "Favorites filter disabled"
			}
		case "F":
			if idx, ok := m.currentRepoIndex(); ok {
				repo := m.repos[idx]
				if m.toggleFavorite(repo.Path) {
					m.status = fmt.Sprintf("Favorited %s in %s", repo.Name, m.activeFavoriteList)
					m.logInfo(fmt.Sprintf("favorite added: %s -> %s", repo.Name, m.activeFavoriteList))
				} else {
					m.status = fmt.Sprintf("Unfavorited %s from %s", repo.Name, m.activeFavoriteList)
					m.logInfo(fmt.Sprintf("favorite removed: %s -> %s", repo.Name, m.activeFavoriteList))
				}
				m.normalizeCursor()
				m.persist()
			}
		case "r":
			if idx, ok := m.currentRepoIndex(); ok {
				repo := m.repos[idx]
				m.refreshRepoStatus(repo.Path)
				m.status = fmt.Sprintf("Refreshed repo status: %s", repo.Name)
				m.logInfo(fmt.Sprintf("refresh: %s (%s)", repo.Name, repo.Path))
			}
		case "R":
			m.refreshRepoStatuses()
			m.status = fmt.Sprintf("Refreshed statuses for %d repositories", len(m.repos))
			m.logInfo(fmt.Sprintf("refresh all: %d repositories", len(m.repos)))
		case "a":
			for i := range m.repos {
				m.repos[i].Selected = true
			}
			m.persist()
			msg := fmt.Sprintf("Selected all %d repositories", len(m.repos))
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
		case "T":
			m.openThemeSelector()
		case "l":
			m.openFavoritesDialog()
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
			selected := selectedRepos(m.repos)
			if len(selected) == 0 {
				m.status = "No repositories selected"
				m.logInfo("Pull: no repositories selected")
				return m, nil
			}
			m.busy = true
			m.status = fmt.Sprintf("Pulling %d repositories...", len(selected))
			m.logInfo(fmt.Sprintf("--- Pull started: %d repos ---", len(selected)))
			for _, r := range selected {
				m.logInfo(fmt.Sprintf("  queued: %s", r.Name))
			}
			m.scrollToBottom(m.outPanelHeight())
			return m, pullSelectedCmd(selected)
		case "x":
			idx, ok := m.currentRepoIndex()
			if !ok {
				return m, nil
			}
			removed := m.repos[idx]
			m.repos = append(m.repos[:idx], m.repos[idx+1:]...)
			m.removeRepoFromFavorites(removed.Path)
			delete(m.repoMeta, removed.Path)
			m.normalizeCursor()
			m.persist()
			m.status = fmt.Sprintf("Removed: %s", removed.Name)
			m.logInfo(fmt.Sprintf("removed: %s (%s)", removed.Name, removed.Path))
			return m, nil
		case "z":
			idx, ok := m.currentRepoIndex()
			if !ok {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[idx]
			cmd := exec.Command("lazygit")
			cmd.Dir = repo.Path
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			m.status = fmt.Sprintf("Launching lazygit: %s", repo.Name)
			m.logInfo(fmt.Sprintf("lazygit: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return lazygitExitedMsg{err: err, path: repo.Path}
			})
		case "v":
			idx, ok := m.currentRepoIndex()
			if !ok {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[idx]
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
	}

	return m, nil
}

// outPanelHeight returns how many output lines are visible given current terminal height.
func (m *Model) outPanelHeight() int {
	bodyH := max(8, m.height-4)
	if m.width < 64 {
		return max(1, bodyH-4)
	}
	topH := max(8, bodyH*2/3)
	outputH := max(5, bodyH-topH)
	return max(1, outputH-4)
}

// leftWidth and rightWidth split the top row with more room for the repo list.
func (m *Model) leftWidth() int {
	if m.width < 64 {
		return m.width
	}
	return (m.width*2)/3 - 1
}

func (m *Model) rightWidth() int {
	if m.width < 64 {
		return 0
	}
	return m.width - m.leftWidth() - 1
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
	header := m.fgStyle(m.theme.Header).
		Bold(true).
		Render(fmt.Sprintf("[%d] %s", number, title))
	headerFill := max(0, innerW-lipgloss.Width(header))
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(borderColor)).
		Background(lipgloss.Color(m.theme.Background))
	top := borderStyle.Render(border.TopLeft) +
		header +
		borderStyle.Render(strings.Repeat(border.Top, headerFill)+border.TopRight)

	content := m.padBackground(m.indentBody(body, innerW), innerW, innerH)
	lines := strings.Split(content, "\n")
	contentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(m.theme.Foreground)).
		Background(lipgloss.Color(m.theme.Background))
	for i, line := range lines {
		lines[i] = borderStyle.Render(border.Left) + contentStyle.Render(line) + borderStyle.Render(border.Right)
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
		valid[repo.Path] = struct{}{}
		m.repoMeta[repo.Path] = gitutil.InspectRepoMetadata(repo.Path)
	}
	for path := range m.repoMeta {
		if _, ok := valid[path]; !ok {
			delete(m.repoMeta, path)
		}
	}
}

func (m *Model) refreshRepoStatus(path string) {
	if m.repoMeta == nil {
		m.repoMeta = map[string]gitutil.RepoMetadata{}
	}
	m.repoMeta[path] = gitutil.InspectRepoMetadata(path)
}

func (m Model) repoStatus(path string) gitutil.RepoStatus {
	if meta, ok := m.repoMeta[path]; ok {
		return meta.Status
	}
	return gitutil.StatusNotCloned
}

func (m Model) repoMetadata(path string) gitutil.RepoMetadata {
	if meta, ok := m.repoMeta[path]; ok {
		return meta
	}
	return gitutil.RepoMetadata{Status: gitutil.StatusNotCloned}
}

func (m Model) buildReposContent(width int, rows int) string {
	branchW := 12
	syncW := 7
	nameW := max(6, min(28, width-(3+1+3+1+6+1+syncW+1+branchW+1)))
	sep := m.bgStyle().Render(" ")
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight(
			padCell("Sel", 3)+" "+padCell("F", 3)+" "+padCell("Name ("+m.activeFavoriteList+")", nameW)+" "+padCell("St", 6)+" "+padCell("↑↓", syncW)+" "+padCell("Branch", branchW),
			width,
		)),
	}

	visible := m.visibleRepoIndexes()
	if len(m.repos) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no repos; press o to add or s to scan)"))
	} else if len(visible) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no repos in current favorites view)"))
	} else {
		availableRows := rows - len(lines)
		if m.inputMode != inputNone {
			availableRows -= 3
		}
		if availableRows < 1 {
			availableRows = 1
		}
		start, end := repoViewportRange(visible, m.cursor, availableRows)
		for _, idx := range visible[start:end] {
			repo := m.repos[idx]
			meta := m.repoMetadata(repo.Path)
			status := meta.Status
			focused := idx == m.cursor && m.focus == focusRepos
			rowBg := m.theme.Background
			if focused {
				rowBg = m.theme.RowFocusBg
				sep = m.bgStyle().Background(lipgloss.Color(rowBg)).Render(" ")
			} else {
				sep = m.bgStyle().Render(" ")
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
			if m.isFavorite(repo.Path) {
				favStyle = m.fgBgStyle(m.theme.Accent, rowBg).Bold(true)
				fav = "*"
			}

			statusStyle := m.fgBgStyle(m.theme.Warning, rowBg)
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

			last := ""
			if repo.LastOp != "" {
				lastStyle := m.fgBgStyle(m.theme.Warning, rowBg)
				if strings.Contains(repo.LastOp, "ok") {
					lastStyle = lastStyle.Foreground(lipgloss.Color(m.theme.Success))
				}
				if strings.Contains(repo.LastOp, "failed") {
					lastStyle = lastStyle.Foreground(lipgloss.Color(m.theme.Error))
				}
				last = sep + lastStyle.Render("["+trimRight(repo.LastOp, 12)+"]")
			}

			name := trimRight(repo.Name, nameW)
			branch := trimRight(meta.CurrentBranch, branchW)
			if branch == "" {
				branch = "-"
			}
			row := strings.Join([]string{
				selStyle.Render(padCell(sel, 3)),
				favStyle.Render(padCell(fav, 3)),
				nameStyle.Render(padCell(name, nameW)),
				statusStyle.Render(padCell(status.Symbol(), 6)),
				m.renderSyncCell(meta, rowBg, syncW),
				branchStyle.Render(padCell(branch, branchW)),
			}, sep)
			row += last
			lines = append(lines, row)
		}
	}

	if m.inputMode != inputNone {
		prompt := "Path"
		if m.inputMode == inputScan {
			prompt = "Scan root"
		}
		input := trimRight(prompt+": "+m.textInput.View(), width)
		lines = append(lines, "", m.fgStyle(m.theme.Input).Render(input), m.fgStyle(m.theme.Muted).Render("Enter=confirm Esc=cancel"))
	}

	return strings.Join(limitLines(lines, rows), "\n")
}

func (m Model) buildRepoInfoContent(width int, rows int) string {
	idx, ok := m.currentRepoIndex()
	if !ok || width < 10 {
		return m.fgStyle(m.theme.Muted).Render("(no repo selected)")
	}

	r := m.repos[idx]
	meta := m.repoMetadata(r.Path)
	lastOp := r.LastOp
	if lastOp == "" {
		lastOp = "none"
	}
	favorite := "no"
	if m.isFavorite(r.Path) {
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
		m.labelValue("Path", r.Path, width),
		m.labelValue("Branch", currentBranch, width),
		m.labelValue("Status", status.Description(), width),
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
		lines = append(lines, style.Render(line))
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
	selectorH := 0
	if m.themeSelecting {
		selectorH = min(9, max(5, len(m.themeNames)+3))
		bodyH = max(6, bodyH-selectorH)
	}
	topH := bodyH
	outputH := 0
	if m.width >= 64 {
		topH = max(8, bodyH*2/3)
		outputH = max(5, bodyH-topH)
	}

	leftPanel := m.renderSection(0, "Repos", m.buildReposContent(max(1, lw-2), max(1, topH-2)), lw, topH, m.focus == focusRepos)

	body := leftPanel
	if rw > 0 {
		infoPanel := m.renderSection(1, "Repo Info", m.buildRepoInfoContent(max(1, rw-2), max(1, topH-2)), rw, topH, m.focus == focusInfo)
		gutter := m.renderGutter(topH)
		topRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, gutter, infoPanel)
		outputPanel := m.renderSection(2, "Command Output", m.buildOutputContent(max(1, m.width-2), max(1, outputH-2)), m.width, outputH, m.focus == focusOutput)
		body = lipgloss.JoinVertical(lipgloss.Left, topRow, outputPanel)
	}
	if m.themeSelecting {
		body = lipgloss.JoinVertical(lipgloss.Left, body, m.renderThemeSelector(max(1, m.width), selectorH))
	}
	if m.showHelp {
		body = m.renderHelpOverlay(body)
	}
	if m.favoritesDialog {
		body = m.renderFavoritesDialog(body)
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
		Render(" [0]/[1]/[2] focus  left/right cycle panels  f filter  F favorite  r/R refresh  l lists  T themes  j/k move/scroll  space toggle  a/A sel/desel  o add  s scan  p pull  x remove  z lazygit  v code  Z zed  ? help  q quit")
	return m.renderApp(lipgloss.JoinVertical(lipgloss.Left, body, status, keys))
}

func (m Model) renderHelpOverlay(base string) string {
	_ = base
	screenW := max(1, m.width)
	screenH := max(1, m.height-2)
	dialogW := min(max(32, screenW-8), 72)
	dialogH := min(max(18, screenH-6), 27)
	dialog := m.renderSection(8, "Help", m.helpView(), dialogW, dialogH, true)

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
	if len(m.themeNames) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no themes available)"))
	} else {
		start := 0
		if m.themeCursor >= rows {
			start = m.themeCursor - rows + 1
		}
		end := min(start+rows, len(m.themeNames))
		for i := start; i < end; i++ {
			name := m.themeNames[i]
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
	footer := m.fgStyle(m.theme.Muted).Render("j/k preview  Enter select  Esc cancel")
	body := strings.Join(append(lines, footer), "\n")
	return m.renderSection(9, "Theme Selector", body, width, height, true)
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

func (m Model) favoritesDialogView(width int, rows int) string {
	lists := m.favoriteListNames()
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight("Enter=use  n=new  x=delete  Esc=close", width)),
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

func (m Model) helpView() string {
	raw := []string{
		"Navigation",
		"  j/k or up/down  Move highlight",
		"  left/right      Cycle focused panel",
		"  space           Toggle selection on highlighted repo",
		"",
		"Favorites",
		"  f               Toggle favorites-only filter",
		"  F               Toggle favorite on highlighted repo",
		"  l               Open favorites list dialog",
		"",
		"Refresh",
		"  r               Refresh highlighted repo status",
		"  R               Refresh all repo statuses",
		"",
		"Selection",
		"  a               Select all repos",
		"  A               Deselect all repos",
		"",
		"Repository actions",
		"  o               Add one repository by path",
		"  s               Scan a directory and add discovered repositories",
		"  p               Pull all selected repositories",
		"  x               Remove highlighted repository from tracking",
		"  z               Launch lazygit for highlighted repository",
		"  v               Open highlighted repository in VS Code",
		"  Z               Open highlighted repository in Zed",
		"",
		"Output panel",
		"  2               Focus command output panel",
		"  j/k             Scroll command output panel",
		"  PgUp / PgDn     Scroll command output panel",
		"",
		"UI",
		"  0               Focus repositories",
		"  1               Focus repo info",
		"  2               Focus command output",
		"  Left / Right    Cycle through panels",
		"  T               Open theme selector",
		"  ?               Toggle this help screen",
		"  q / Ctrl+C      Quit",
		"",
		"Press Enter, Esc, or ? to close",
	}
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		switch {
		case line == "":
			lines = append(lines, "")
		case !strings.HasPrefix(line, " "):
			lines = append(lines, m.fgStyle(m.theme.Accent).Bold(true).Render(line))
		case strings.HasPrefix(line, "Press "):
			lines = append(lines, m.fgStyle(m.theme.Muted).Render(line))
		default:
			lines = append(lines, m.fgStyle(m.theme.Foreground).Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) labelValue(label string, value string, width int) string {
	labelStyle := m.fgStyle(m.theme.Accent).Bold(true)
	valueStyle := m.fgStyle(m.theme.Foreground)
	prefix := label + ": "
	if label == "" {
		prefix = "  "
	}
	return labelStyle.Render(prefix) + valueStyle.Render(trimRight(value, max(1, width-len(prefix))))
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
	for i, name := range m.themeNames {
		if name == m.themeName {
			m.themeCursor = i
			break
		}
	}
	m.status = "Theme selector: preview with j/k, Enter selects, Esc cancels"
}

func (m Model) handleThemeSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
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
	if len(m.themeNames) == 0 {
		return
	}
	m.themeCursor = (m.themeCursor + delta + len(m.themeNames)) % len(m.themeNames)
	name := m.themeNames[m.themeCursor]
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

func (m Model) visibleRepoIndexes() []int {
	indexes := make([]int, 0, len(m.repos))
	for i, repo := range m.repos {
		if m.favoritesOnly && !m.isFavorite(repo.Path) {
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
		return
	}
	if !m.favoritesOnly {
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor >= len(m.repos) {
			m.cursor = len(m.repos) - 1
		}
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
		return
	}
	for _, idx := range visible {
		if idx == m.cursor {
			return
		}
	}
	m.cursor = visible[0]
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
}

func repoViewportRange(visible []int, cursor int, rows int) (start int, end int) {
	if len(visible) == 0 {
		return 0, 0
	}
	if rows <= 0 || rows >= len(visible) {
		return 0, len(visible)
	}

	cursorPos := 0
	for i, idx := range visible {
		if idx == cursor {
			cursorPos = i
			break
		}
	}

	start = cursorPos - rows + 1
	if start < 0 {
		start = 0
	}
	end = start + rows
	if end > len(visible) {
		end = len(visible)
		start = max(0, end-rows)
	}
	if cursorPos < start {
		start = cursorPos
		end = min(len(visible), start+rows)
	}
	if cursorPos >= end {
		end = cursorPos + 1
		start = max(0, end-rows)
	}
	return start, end
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
	m.status = "Favorites lists: Enter selects, n creates, x deletes, Esc closes"
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
	case "esc", "q", "l":
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

func (m Model) handleInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		if r.Path == path {
			return false, nil
		}
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
	m.refreshRepoStatus(path)
	m.normalizeCursor()
	m.persist()
	return true, nil
}

func (m *Model) persist() {
	if m.store == nil {
		return
	}
	state := store.State{
		Repos:              m.repos,
		FavoriteLists:      m.favoriteListsForStore(),
		ActiveFavoriteList: m.activeFavoriteList,
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
					path:   repos[i].Path,
					output: out,
					err:    err,
				}
			}(i)
		}

		wg.Wait()
		return pullFinishedMsg{results: results}
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
			path:     repo.Path,
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
