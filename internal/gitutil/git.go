package gitutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RepoStatus int

type RepoMetadata struct {
	Status           RepoStatus
	CurrentBranch    string
	LocalBranches    []string
	AheadCount       int
	BehindCount      int
	HasUpstream      bool
	LastCommitAuthor string
	LastCommitAt     time.Time
}

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

func PullCommand(path string) string {
	return fmt.Sprintf("git -C %s pull --ff-only", strconv.Quote(path))
}

func Fetch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "fetch", "--all", "--prune")
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
		return combined, fmt.Errorf("fetch failed: %w", err)
	}
	return combined, nil
}

func FetchCommand(path string) string {
	return fmt.Sprintf("git -C %s fetch --all --prune", strconv.Quote(path))
}

func Clone(remoteURL string, path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	cmd := exec.Command("git", "clone", remoteURL, path)
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
		return combined, fmt.Errorf("clone failed: %w", err)
	}
	return combined, nil
}

func CloneCommand(remoteURL string, path string) string {
	return fmt.Sprintf("git clone %s %s", strconv.Quote(remoteURL), strconv.Quote(path))
}

func InspectStatus(path string) RepoStatus {
	return InspectRepoMetadata(path).Status
}

func InspectRepoMetadata(path string) RepoMetadata {
	if _, err := os.Stat(path); err != nil {
		return RepoMetadata{Status: StatusNotCloned}
	}
	if !IsGitRepo(path) {
		return RepoMetadata{Status: StatusNotCloned}
	}

	cmd := exec.Command("git", "-C", path, "status", "--porcelain")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return RepoMetadata{Status: StatusNotCloned}
	}

	meta := RepoMetadata{
		Status:        inspectPorcelainStatus(strings.TrimSpace(stdout.String())),
		CurrentBranch: currentBranch(path),
		LocalBranches: localBranches(path),
	}
	meta.AheadCount, meta.BehindCount, meta.HasUpstream = upstreamDivergence(path)
	meta.LastCommitAuthor, meta.LastCommitAt = lastCommitInfo(path)
	return meta
}

func inspectPorcelainStatus(output string) RepoStatus {
	output = strings.TrimSpace(output)
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

func currentBranch(path string) string {
	cmd := exec.Command("git", "-C", path, "branch", "--show-current")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func localBranches(path string) []string {
	cmd := exec.Command("git", "-C", path, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		branches = append(branches, name)
	}
	sort.Strings(branches)
	return branches
}

func upstreamDivergence(path string) (ahead int, behind int, hasUpstream bool) {
	cmd := exec.Command("git", "-C", path, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return 0, 0, false
	}

	fields := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(fields) != 2 {
		return 0, 0, false
	}

	if _, err := fmt.Sscanf(fields[0], "%d", &behind); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(fields[1], "%d", &ahead); err != nil {
		return 0, 0, false
	}

	return ahead, behind, true
}

func lastCommitInfo(path string) (author string, committedAt time.Time) {
	cmd := exec.Command("git", "-C", path, "log", "-1", "--format=%ct%x09%an")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", time.Time{}
	}

	fields := strings.SplitN(strings.TrimSpace(stdout.String()), "\t", 2)
	if len(fields) != 2 {
		return "", time.Time{}
	}

	unixSeconds, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
	if err != nil {
		return "", time.Time{}
	}

	return strings.TrimSpace(fields[1]), time.Unix(unixSeconds, 0)
}
