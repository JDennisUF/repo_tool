package gerrit

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct {
	Username string
	Server   string
	BaseDir  string
}

func (c Config) Target() string {
	server := strings.TrimSpace(c.Server)
	username := strings.TrimSpace(c.Username)
	if username == "" {
		return server
	}
	return username + "@" + server
}

func (c Config) ValidateForListing() error {
	if strings.TrimSpace(c.Server) == "" {
		return fmt.Errorf("gerrit server is required")
	}
	return nil
}

func (c Config) ValidateForClone() error {
	if err := c.ValidateForListing(); err != nil {
		return err
	}
	if strings.TrimSpace(c.BaseDir) == "" {
		return fmt.Errorf("base git directory is required")
	}
	return nil
}

func ListProjectsCommand(target string) string {
	return fmt.Sprintf("ssh %s gerrit ls-projects", shellQuote(strings.TrimSpace(target)))
}

func BuildCloneURL(target string, project string) string {
	return "ssh://" + strings.TrimSpace(target) + "/" + strings.TrimSpace(project)
}

func LocalPath(baseDir string, project string) string {
	cleanProject := strings.Trim(strings.TrimSpace(project), "/")
	parts := strings.Split(cleanProject, "/")
	return filepath.Join(append([]string{filepath.Clean(baseDir)}, parts...)...)
}

func ParseProjects(output string) []string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	seen := map[string]struct{}{}
	projects := []string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		project := strings.Trim(strings.TrimSpace(fields[0]), "/")
		if project == "" {
			continue
		}
		if _, ok := seen[project]; ok {
			continue
		}
		seen[project] = struct{}{}
		projects = append(projects, project)
	}
	return projects
}

func ListProjects(cfg Config) ([]string, string, error) {
	if err := cfg.ValidateForListing(); err != nil {
		return nil, "", err
	}

	cmd := exec.Command("ssh", cfg.Target(), "gerrit", "ls-projects")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())
	combined := strings.TrimSpace(strings.Join([]string{out, errOut}, "\n"))
	if err != nil {
		if combined == "" {
			combined = err.Error()
		}
		return nil, combined, fmt.Errorf("gerrit ls-projects failed: %w", err)
	}
	return ParseProjects(out), combined, nil
}

func shellQuote(value string) string {
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\"'") {
		return value
	}
	return fmt.Sprintf("%q", value)
}
