package gitutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

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
