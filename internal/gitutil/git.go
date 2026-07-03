package gitutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type RepoStatus int

const (
	StatusNotCloned RepoStatus = iota
	StatusUntrackedFiles
	StatusUncommittedChanges
	StatusCurrent
)

func (s RepoStatus) Symbol() string {
	switch s {
	case StatusCurrent:
		return "✓"
	case StatusUncommittedChanges:
		return "!"
	case StatusUntrackedFiles:
		return "+"
	default:
		return "?"
	}
}

func (s RepoStatus) ShortLabel() string {
	switch s {
	case StatusCurrent:
		return "Current"
	case StatusUncommittedChanges:
		return "Dirty"
	case StatusUntrackedFiles:
		return "Untracked"
	default:
		return "Not Cloned"
	}
}

func (s RepoStatus) Description() string {
	switch s {
	case StatusCurrent:
		return "Current"
	case StatusUncommittedChanges:
		return "Uncommitted Changes"
	case StatusUntrackedFiles:
		return "Untracked Files"
	default:
		return "Not Cloned"
	}
}

func IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err == nil {
		return true
	}

	cmd = exec.Command("git", "-C", path, "rev-parse", "--is-bare-repository")
	return cmd.Run() == nil
}

func Pull(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "pull", "--ff-only")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())
	combined := strings.TrimSpace(strings.Join([]string{out, errOut}, "\n"))
	if combined == "" {
		combined = "no output"
	}

	if err != nil {
		return combined, fmt.Errorf("pull failed: %w", err)
	}
	return combined, nil
}

func InspectStatus(path string) RepoStatus {
	if _, err := os.Stat(path); err != nil {
		return StatusNotCloned
	}
	if !IsGitRepo(path) {
		return StatusNotCloned
	}

	cmd := exec.Command("git", "-C", path, "status", "--porcelain")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return StatusNotCloned
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return StatusCurrent
	}

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "?? ") {
			return StatusUntrackedFiles
		}
	}

	return StatusUncommittedChanges
}
