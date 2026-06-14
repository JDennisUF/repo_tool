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

	// output panel
	output    []outputLine
	outScroll int // index of first visible line

	store     *store.Store
	textInput textinput.Model
	inputMode inputMode
}

func NewModel() Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 2048
	ti.Width = 80

	s, err := store.New()
	m := Model{
		store:     s,
		textInput: ti,
		status:    "Ready",
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

	case tea.KeyMsg:
		if m.inputMode != inputNone {
			return m.handleInputMode(msg)
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.repos)-1 {
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
	// reserve: 2 title rows + 1 header + 1 status bar + 1 key hint + 2 padding
	reserved := 7
	if m.inputMode != inputNone {
		reserved += 2
	}
	h := m.height - reserved
	if h < 3 {
		return 3
	}
	return h
}

// leftWidth and rightWidth split the terminal roughly 50/50 with a 1-char gutter.
func (m *Model) leftWidth() int {
	if m.width < 40 {
		return m.width
	}
	return m.width/2 - 1
}

func (m *Model) rightWidth() int {
	if m.width < 40 {
		return 0
	}
	return m.width - m.leftWidth() - 1
}

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}

	lw := m.leftWidth()
	rw := m.rightWidth()
	panelH := m.outPanelHeight()

	// ---------- build left column lines ----------
	leftLines := []string{
		" rt - repo tool",
		"",
		" Repos",
		" " + trimRight("Sel  Name", lw-1),
		" " + strings.Repeat("-", max(0, lw-2)),
	}

	if len(m.repos) == 0 {
		leftLines = append(leftLines, "  (no repos; press o to add or s to scan)")
	} else {
		for i, repo := range m.repos {
			cur := " "
			if i == m.cursor {
				cur = ">"
			}
			sel := "[ ]"
			if repo.Selected {
				sel = "[x]"
			}
			last := ""
			if repo.LastOp != "" {
				last = " [" + repo.LastOp + "]"
			}
			nameW := max(0, lw-9)
			line := fmt.Sprintf("%s %s %s%s", cur, sel, trimRight(repo.Name, nameW), last)
			leftLines = append(leftLines, " "+trimRight(line, lw-1))
		}
	}

	if m.inputMode != inputNone {
		prompt := "Path"
		if m.inputMode == inputScan {
			prompt = "Scan root"
		}
		leftLines = append(leftLines, "", " "+prompt+": "+m.textInput.View(), " Enter=confirm Esc=cancel")
	}

	// ---------- build right column lines ----------
	rightLines := []string{
		" Command Output",
		" " + strings.Repeat("-", max(0, rw-2)),
	}

	// visible slice of output buffer
	start := m.outScroll
	if start > len(m.output) {
		start = len(m.output)
	}
	end := start + (panelH - len(rightLines))
	if end > len(m.output) {
		end = len(m.output)
	}
	visible := m.output[start:end]

	for _, ol := range visible {
		prefix := "  "
		if ol.fail {
			prefix = "! "
		}
		line := prefix + ol.ts + " " + ol.text
		rightLines = append(rightLines, " "+trimRight(line, rw-1))
	}

	// scroll indicator
	if len(m.output) > 0 {
		indicator := fmt.Sprintf(" [%d-%d / %d]", start+1, start+len(visible), len(m.output))
		rightLines = append(rightLines, trimRight(indicator, rw))
	}

	// ---------- merge columns side by side ----------
	var b strings.Builder
	totalRows := max(len(leftLines), len(rightLines))
	divider := "|"
	if rw == 0 {
		divider = ""
	}

	for i := 0; i < totalRows; i++ {
		lLine := ""
		if i < len(leftLines) {
			lLine = leftLines[i]
		}
		rLine := ""
		if i < len(rightLines) {
			rLine = rightLines[i]
		}

		if rw > 0 {
			paddedL := padRight(lLine, lw)
			b.WriteString(paddedL + divider + rLine + "\n")
		} else {
			b.WriteString(lLine + "\n")
		}
	}

	// ---------- status bar ----------
	busy := "idle"
	if m.busy {
		busy = "busy"
	}
	b.WriteString("\n")
	b.WriteString(" Status: " + m.status + " [" + busy + "]\n")
	b.WriteString(" j/k move  space toggle  a/A sel/desel-all  o add  s scan  p pull  z lazygit  PgUp/PgDn scroll  ? help  q quit\n")
	return b.String()
}

func (m Model) helpView() string {
	return strings.Join([]string{
		"rt - repo tool help",
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
		"  z               Launch lazygit for highlighted repository",
		"",
		"Output panel",
		"  PgUp / PgDn     Scroll command output panel",
		"",
		"UI",
		"  ?               Toggle this help screen",
		"  q / Ctrl+C      Quit",
		"",
		"Press ? to close help",
	}, "\n")
}

func padRight(s string, width int) string {
	r := []rune(s)
	if len(r) >= width {
		return string(r[:width])
	}
	return s + strings.Repeat(" ", width-len(r))
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
