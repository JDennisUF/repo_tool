package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInspectStatusNotCloned(t *testing.T) {
	t.Parallel()

	if got := InspectStatus(filepath.Join(t.TempDir(), "missing")); got != StatusNotCloned {
		t.Fatalf("status = %v, want %v", got, StatusNotCloned)
	}
}

func TestInspectStatusCurrent(t *testing.T) {
	repo := initTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "hello\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	if got := InspectStatus(repo); got != StatusCurrent {
		t.Fatalf("status = %v, want %v", got, StatusCurrent)
	}
}

func TestInspectStatusUncommittedChanges(t *testing.T) {
	repo := initTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "hello\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")

	if got := InspectStatus(repo); got != StatusUncommittedChanges {
		t.Fatalf("status = %v, want %v", got, StatusUncommittedChanges)
	}
}

func TestInspectStatusUntrackedFiles(t *testing.T) {
	repo := initTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "hello\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")

	if got := InspectStatus(repo); got != StatusUntrackedFiles {
		t.Fatalf("status = %v, want %v", got, StatusUntrackedFiles)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
