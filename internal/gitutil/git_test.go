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

func TestInspectRepoMetadataBranches(t *testing.T) {
	repo := initTestRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "hello\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGit(t, repo, "checkout", "-b", "feature/test")
	runGit(t, repo, "branch", "bugfix/test")

	meta := InspectRepoMetadata(repo)
	if meta.Status != StatusCurrent {
		t.Fatalf("status = %v, want %v", meta.Status, StatusCurrent)
	}
	if meta.CurrentBranch != "feature/test" {
		t.Fatalf("current branch = %q, want %q", meta.CurrentBranch, "feature/test")
	}
	if len(meta.LocalBranches) < 3 {
		t.Fatalf("local branch count = %d, want at least 3 (%v)", len(meta.LocalBranches), meta.LocalBranches)
	}
	if !containsString(meta.LocalBranches, "bugfix/test") || !containsString(meta.LocalBranches, "feature/test") {
		t.Fatalf("local branches = %v, want bugfix/test and feature/test", meta.LocalBranches)
	}
	if meta.LastCommitAuthor != "Test" {
		t.Fatalf("last commit author = %q, want %q", meta.LastCommitAuthor, "Test")
	}
	if meta.LastCommitAt.IsZero() {
		t.Fatal("expected last commit time to be populated")
	}
}

func TestInspectRepoMetadataUpstreamDivergence(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)

	seed := t.TempDir()
	runGit(t, "", "init", seed)
	writeFile(t, filepath.Join(seed, "tracked.txt"), "hello\n")
	runGit(t, seed, "add", "tracked.txt")
	runGit(t, seed, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	clone1 := filepath.Join(t.TempDir(), "clone1")
	clone2 := filepath.Join(t.TempDir(), "clone2")
	runGit(t, "", "clone", remote, clone1)
	runGit(t, "", "clone", remote, clone2)

	writeFile(t, filepath.Join(clone1, "ahead.txt"), "ahead\n")
	runGit(t, clone1, "add", "ahead.txt")
	runGit(t, clone1, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "ahead")

	writeFile(t, filepath.Join(clone2, "behind.txt"), "behind\n")
	runGit(t, clone2, "add", "behind.txt")
	runGit(t, clone2, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "behind")
	runGit(t, clone2, "push")
	runGit(t, clone1, "fetch", "origin")

	meta := InspectRepoMetadata(clone1)
	if !meta.HasUpstream {
		t.Fatal("expected upstream metadata")
	}
	if meta.AheadCount != 1 || meta.BehindCount != 1 {
		t.Fatalf("ahead/behind = %d/%d, want 1/1", meta.AheadCount, meta.BehindCount)
	}
}

func TestPullCommand(t *testing.T) {
	repo := "/tmp/repo with spaces"
	if got, want := PullCommand(repo), `git -C "/tmp/repo with spaces" pull --ff-only`; got != want {
		t.Fatalf("pull command = %q, want %q", got, want)
	}
}

func TestFetchCommand(t *testing.T) {
	repo := "/tmp/repo with spaces"
	if got, want := FetchCommand(repo), `git -C "/tmp/repo with spaces" fetch --all --prune`; got != want {
		t.Fatalf("fetch command = %q, want %q", got, want)
	}
}

func TestCloneCommand(t *testing.T) {
	remoteURL := "ssh://alice@gerrit.example.com/team/repo"
	path := "/tmp/repo with spaces"
	if got, want := CloneCommand(remoteURL, path), `git clone "ssh://alice@gerrit.example.com/team/repo" "/tmp/repo with spaces"`; got != want {
		t.Fatalf("clone command = %q, want %q", got, want)
	}
}

func TestClone(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)

	seed := t.TempDir()
	runGit(t, "", "init", seed)
	writeFile(t, filepath.Join(seed, "tracked.txt"), "hello\n")
	runGit(t, seed, "add", "tracked.txt")
	runGit(t, seed, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-u", "origin", "main")
	runGit(t, "", "--git-dir", remote, "symbolic-ref", "HEAD", "refs/heads/main")

	target := filepath.Join(t.TempDir(), "nested", "clone")
	if _, err := Clone(remote, target); err != nil {
		t.Fatalf("clone failed: %v", err)
	}
	if !IsGitRepo(target) {
		t.Fatalf("expected git repo at %s", target)
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
	gitArgs := args
	if repo != "" {
		gitArgs = append([]string{"-C", repo}, args...)
	}
	cmd := exec.Command("git", gitArgs...)
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
