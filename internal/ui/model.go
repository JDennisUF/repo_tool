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

type focusSection int

const (
	focusRepos focusSection = iota
	focusInfo
	focusOutput
)

type pullResult struct {
	path   string
	output string
	err    error
}

type pullFinishedMsg struct {
	results []pullResult
}

type lazygitExitedMsg struct {
	err error
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

	store          *store.Store
	textInput      textinput.Model
	inputMode      inputMode
	theme          themePalette
	themeName      string
	themes         map[string]themePalette
	themeNames     []string
	themeSelecting bool
	themeCursor    int
	savedTheme     themePalette
	savedThemeName string
}

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

	repos, loadErr := s.Load()
	if loadErr != nil {
		m.status = fmt.Sprintf("Load error: %v", loadErr)
		return m
	}
	m.repos = repos
	m.logInfo(fmt.Sprintf("Loaded %d repositories", len(repos)))
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

	case lazygitExitedMsg:
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
			m.focus = focusRepos
			m.status = "Focused [0] Repos"
		case "1":
			m.focus = focusInfo
			m.status = "Focused [1] Repo Info"
		case "2":
			m.focus = focusOutput
			m.status = "Focused [2] Command Output"
		case "up", "k":
			if m.focus == focusOutput {
				m.outScroll = max(0, m.outScroll-1)
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.focus == focusOutput {
				limit := max(0, len(m.output)-m.outPanelHeight())
				m.outScroll = min(m.outScroll+1, limit)
			} else if m.cursor < len(m.repos)-1 {
				m.cursor++
			}
		case " ":
			if len(m.repos) > 0 {
				m.repos[m.cursor].Selected = !m.repos[m.cursor].Selected
				m.persist()
			}
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
			if len(m.repos) == 0 {
				return m, nil
			}
			removed := m.repos[m.cursor]
			m.repos = append(m.repos[:m.cursor], m.repos[m.cursor+1:]...)
			if m.cursor > 0 && m.cursor >= len(m.repos) {
				m.cursor = len(m.repos) - 1
			}
			m.persist()
			m.status = fmt.Sprintf("Removed: %s", removed.Name)
			m.logInfo(fmt.Sprintf("removed: %s (%s)", removed.Name, removed.Path))
			return m, nil
		case "z":
			if len(m.repos) == 0 {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[m.cursor]
			cmd := exec.Command("lazygit")
			cmd.Dir = repo.Path
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			m.status = fmt.Sprintf("Launching lazygit: %s", repo.Name)
			m.logInfo(fmt.Sprintf("lazygit: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return lazygitExitedMsg{err: err}
			})
		case "v":
			if len(m.repos) == 0 {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[m.cursor]
			m.status = fmt.Sprintf("Opening VS Code: %s", repo.Name)
			m.logInfo(fmt.Sprintf("code: opening %s (%s)", repo.Name, repo.Path))
			m.scrollToBottom(m.outPanelHeight())
			return m, openEditorCmd("VS Code", "code", repo)
		case "Z":
			if len(m.repos) == 0 {
				m.status = "No repository highlighted"
				return m, nil
			}
			repo := m.repos[m.cursor]
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
	if m.rightWidth() == 0 {
		return max(1, bodyH-4)
	}
	infoH := min(8, max(5, bodyH/3))
	outputH := max(5, bodyH-infoH)
	return max(1, outputH-4)
}

// leftWidth and rightWidth split the terminal roughly 50/50 with a 1-char gutter.
func (m *Model) leftWidth() int {
	if m.width < 64 {
		return m.width
	}
	return m.width/2 - 1
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
	header := m.fgStyle(m.theme.Header).
		Bold(true).
		Render(fmt.Sprintf("[%d] %s", number, title))
	content := header
	if body != "" {
		content += "\n" + body
	}
	content = m.padBackground(content, max(1, width-2), max(1, height-2))
	return m.sectionStyle(width, height, focused).Render(content)
}

func (m Model) buildReposContent(width int, rows int) string {
	lines := []string{
		m.fgStyle(m.theme.Muted).Render(trimRight("Sel  Name", width)),
	}

	if len(m.repos) == 0 {
		lines = append(lines, m.fgStyle(m.theme.Muted).Render("(no repos; press o to add or s to scan)"))
	} else {
		nameW := max(6, width-15)
		for i, repo := range m.repos {
			cursor := " "
			if i == m.cursor {
				cursor = m.fgStyle(m.theme.Cursor).Bold(true).Render(">")
			}
			sel := "[ ]"
			if repo.Selected {
				sel = m.fgStyle(m.theme.Selection).Bold(true).Render("[x]")
			}
			last := ""
			if repo.LastOp != "" {
				lastStyle := m.fgStyle(m.theme.Warning)
				if strings.Contains(repo.LastOp, "ok") {
					lastStyle = lastStyle.Foreground(lipgloss.Color(m.theme.Success))
				}
				if strings.Contains(repo.LastOp, "failed") {
					lastStyle = lastStyle.Foreground(lipgloss.Color(m.theme.Error))
				}
				last = " " + lastStyle.Render("["+trimRight(repo.LastOp, 12)+"]")
			}
			name := trimRight(repo.Name, nameW)
			row := fmt.Sprintf("%s %s %s%s", cursor, sel, name, last)
			if i == m.cursor && m.focus == focusRepos {
				row = m.fgStyle(m.theme.Accent).Bold(true).Render(row)
			}
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
	if len(m.repos) == 0 || width < 10 {
		return m.fgStyle(m.theme.Muted).Render("(no repo selected)")
	}

	r := m.repos[m.cursor]
	lastOp := r.LastOp
	if lastOp == "" {
		lastOp = "none"
	}
	lines := []string{
		m.labelValue("Name", r.Name, width),
		m.labelValue("Path", r.Path, width),
		m.labelValue("Last", lastOp, width),
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
	leftPanel := m.renderSection(0, "Repos", m.buildReposContent(max(1, lw-2), max(1, bodyH-3)), lw, bodyH, m.focus == focusRepos)

	body := leftPanel
	if rw > 0 {
		infoH := min(8, max(5, bodyH/3))
		outputH := max(5, bodyH-infoH)
		infoPanel := m.renderSection(1, "Repo Info", m.buildRepoInfoContent(max(1, rw-2), max(1, infoH-3)), rw, infoH, m.focus == focusInfo)
		outputPanel := m.renderSection(2, "Command Output", m.buildOutputContent(max(1, rw-2), max(1, outputH-3)), rw, outputH, m.focus == focusOutput)
		rightPanel := lipgloss.JoinVertical(lipgloss.Left, infoPanel, outputPanel)
		gutter := m.renderGutter(bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, gutter, rightPanel)
	}
	if m.themeSelecting {
		body = lipgloss.JoinVertical(lipgloss.Left, body, m.renderThemeSelector(max(1, m.width), selectorH))
	}
	if m.showHelp {
		body = m.renderHelpOverlay(body)
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
		Render(" Status: " + m.status + " [" + busy + "] theme=" + m.themeName)
	keys := m.fgStyle(m.theme.Muted).
		Width(max(1, m.width)).
		Render(" [0]/[1]/[2] focus  T themes  j/k move/scroll  space toggle  a/A sel/desel  o add  s scan  p pull  x remove  z lazygit  v code  Z zed  ? help  q quit")
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
	rows := max(1, height-3)
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

func (m Model) helpView() string {
	return strings.Join([]string{
		"rt help",
		"",
		"Navigation",
		"  j/k or up/down  Move highlight",
		"  space           Toggle selection on highlighted repo",
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
		"  j/k             Scroll output when output panel is focused",
		"  PgUp / PgDn     Scroll command output panel",
		"",
		"UI",
		"  0               Focus repositories",
		"  1               Focus repo info",
		"  T               Open theme selector",
		"  ?               Toggle this help screen",
		"  q / Ctrl+C      Quit",
		"",
		"Press Enter, Esc, or ? to close",
	}, "\n")
}

func (m Model) labelValue(label string, value string, width int) string {
	labelStyle := m.fgStyle(m.theme.Accent).Bold(true)
	valueStyle := m.fgStyle(m.theme.Foreground)
	prefix := label + ": "
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
	m.persist()
	return true, nil
}

func (m *Model) persist() {
	if m.store == nil {
		return
	}
	if err := m.store.Save(m.repos); err != nil {
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
