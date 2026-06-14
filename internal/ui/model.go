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

type Model struct {
	repos    []store.Repo
	cursor   int
	width    int
	height   int
	status   string
	busy     bool
	showHelp bool

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
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textInput.Width = max(20, msg.Width-10)
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
				} else {
					m.repos[i].LastOp = "pull ok"
					successes++
				}
			}
		}
		m.persist()
		m.status = fmt.Sprintf("Pull complete: %d ok, %d failed", successes, failures)
		return m, nil

	case lazygitExitedMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("lazygit failed: %v", msg.err)
		} else {
			m.status = "Returned from lazygit"
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
			m.status = fmt.Sprintf("Selected %d repositories", len(m.repos))
		case "A":
			for i := range m.repos {
				m.repos[i].Selected = false
			}
			m.persist()
			m.status = "Deselected all repositories"
		case "?":
			m.showHelp = !m.showHelp
		case "o":
			m.inputMode = inputAddOne
			m.textInput.SetValue("")
			m.textInput.Focus()
			m.status = "Add repo: enter path (drag-drop works as pasted path)"
		case "s":
			m.inputMode = inputScan
			m.textInput.SetValue("")
			m.textInput.Focus()
			m.status = "Scan for repos: enter root directory"
		case "p":
			if m.busy {
				m.status = "Busy running pull"
				return m, nil
			}
			selected := selectedRepos(m.repos)
			if len(selected) == 0 {
				m.status = "No repositories selected"
				return m, nil
			}
			m.busy = true
			m.status = fmt.Sprintf("Pulling %d repositories...", len(selected))
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
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return lazygitExitedMsg{err: err}
			})
		}
	}

	return m, nil
}

func (m Model) View() string {
	if m.showHelp {
		return m.helpView()
	}

	var b strings.Builder
	b.WriteString("repotui\n\n")
	b.WriteString("Repos\n")
	b.WriteString("  Sel  Name                       Path                               Last\n")
	b.WriteString("  ---  -------------------------  ---------------------------------  -----------\n")

	if len(m.repos) == 0 {
		b.WriteString("  (no repositories tracked; press o to add one or s to scan)\n")
	} else {
		for i, repo := range m.repos {
			cursor := " "
			if i == m.cursor {
				cursor = ">"
			}
			sel := "[ ]"
			if repo.Selected {
				sel = "[x]"
			}
			name := trimRight(repo.Name, 25)
			path := trimRight(repo.Path, 33)
			last := trimRight(repo.LastOp, 11)
			b.WriteString(fmt.Sprintf("%s %s  %-25s  %-33s  %-11s\n", cursor, sel, name, path, last))
		}
	}

	if m.inputMode != inputNone {
		prompt := "Path"
		if m.inputMode == inputScan {
			prompt = "Scan root"
		}
		b.WriteString("\n")
		b.WriteString(prompt + ": " + m.textInput.View() + "\n")
		b.WriteString("Enter=confirm Esc=cancel\n")
	}

	busy := "idle"
	if m.busy {
		busy = "busy"
	}
	b.WriteString("\n")
	b.WriteString("Status: " + m.status + " [" + busy + "]\n")
	b.WriteString("Keys: j/k or up/down move | space toggle | a select-all | A deselect-all | o add | s scan | p pull selected | z lazygit | ? help | q quit\n")
	return b.String()
}

func (m Model) helpView() string {
	return strings.Join([]string{
		"repotui help",
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
		"UI",
		"  ?               Toggle this help screen",
		"  q / Ctrl+C      Quit",
		"",
		"Press ? to close help",
	}, "\n")
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
				return m, nil
			}
			if !added {
				m.status = "Repository already tracked"
				return m, nil
			}
			m.status = "Repository added"
			return m, nil
		}

		if m.inputMode == inputScan {
			root, err := expandPath(value)
			if err != nil {
				m.inputMode = inputNone
				m.status = fmt.Sprintf("Invalid path: %v", err)
				return m, nil
			}
			repos, err := discovery.ScanGitRepos(root)
			if err != nil {
				m.inputMode = inputNone
				m.status = fmt.Sprintf("Scan failed: %v", err)
				return m, nil
			}
			addedCount := 0
			for _, path := range repos {
				added, addErr := m.addRepo(path)
				if addErr == nil && added {
					addedCount++
				}
			}
			m.inputMode = inputNone
			m.status = fmt.Sprintf("Scan complete: %d new repos added", addedCount)
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
