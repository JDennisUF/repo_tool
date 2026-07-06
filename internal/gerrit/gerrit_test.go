package gerrit

import "testing"

func TestTarget(t *testing.T) {
	t.Parallel()

	cfg := Config{Username: "alice", Server: "gerrit.example.com"}
	if got, want := cfg.Target(), "alice@gerrit.example.com"; got != want {
		t.Fatalf("target = %q, want %q", got, want)
	}
}

func TestParseProjectsDeduplicatesAndSkipsBlankLines(t *testing.T) {
	t.Parallel()

	out := "\nproj/a\nproj/b\nproj/a\n\n"
	got := ParseProjects(out)
	want := []string{"proj/a", "proj/b"}
	if len(got) != len(want) {
		t.Fatalf("project count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("project[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseProjectsUsesFirstFieldOnly(t *testing.T) {
	t.Parallel()

	out := "ehrexp/ehrexp-windows-service master\nteam/repo\tdraft\n"
	got := ParseProjects(out)
	want := []string{"ehrexp/ehrexp-windows-service", "team/repo"}
	if len(got) != len(want) {
		t.Fatalf("project count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("project[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildCloneURL(t *testing.T) {
	t.Parallel()

	if got, want := BuildCloneURL("alice@gerrit.example.com", "proj/a"), "ssh://alice@gerrit.example.com/proj/a"; got != want {
		t.Fatalf("clone url = %q, want %q", got, want)
	}
}

func TestLocalPath(t *testing.T) {
	t.Parallel()

	if got, want := LocalPath("/src/git", "/proj/a/"), "/src/git/proj/a"; got != want {
		t.Fatalf("local path = %q, want %q", got, want)
	}
}
