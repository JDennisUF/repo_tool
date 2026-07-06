package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyRepoState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	data := []byte(`{
  "repos": [
    {"name":"b","path":"/tmp/b"},
    {"name":"a","path":"/tmp/a"},
    {"name":"dup","path":"/tmp/a"},
    {"name":"","path":"/tmp/c"}
  ]
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	s := &Store{path: path}
	state, err := s.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if got, want := len(state.Repos), 3; got != want {
		t.Fatalf("repo count = %d, want %d", got, want)
	}
	if state.Repos[0].Name != "a" || state.Repos[1].Name != "b" || state.Repos[2].Name != "c" {
		t.Fatalf("unexpected repo order/names: %+v", state.Repos)
	}
	if state.ActiveFavoriteList != defaultFavoriteListName {
		t.Fatalf("active favorite list = %q, want %q", state.ActiveFavoriteList, defaultFavoriteListName)
	}
	if _, ok := state.FavoriteLists[defaultFavoriteListName]; !ok {
		t.Fatalf("default favorites list missing: %+v", state.FavoriteLists)
	}
	if state.Settings.ShowGitCommands {
		t.Fatal("show git commands should default to false")
	}
	if !state.Settings.ShowRepoInfo {
		t.Fatal("show repo info should default to true for legacy state")
	}
	if state.Settings.GerritUsername != "" || state.Settings.GerritServer != "" || state.Settings.BaseGitDir != "" {
		t.Fatalf("unexpected gerrit settings in legacy state: %+v", state.Settings)
	}
}

func TestSaveLoadPreservesFavoriteLists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	s := &Store{path: path}

	in := State{
		Repos: []Repo{
			{Name: "repo", Path: "/tmp/repo", Selected: true, LastOp: "pull ok", LastUpdated: "2026-07-02T10:11:12Z"},
			{Name: "proj", Path: "/tmp/git/proj", GerritProject: "team/proj", RemoteURL: "ssh://alice@gerrit/team/proj"},
		},
		FavoriteLists: map[string][]string{
			"work":     {"/tmp/repo", "/tmp/repo"},
			"personal": {"/tmp/other"},
		},
		ActiveFavoriteList: "work",
		Settings: Settings{
			ShowGitCommands: true,
			ShowRepoInfo:    true,
			GerritUsername:  "alice",
			GerritServer:    "gerrit.example.com",
			BaseGitDir:      "/tmp/git",
		},
	}
	if err := s.Save(in); err != nil {
		t.Fatalf("save state: %v", err)
	}

	out, err := s.Load()
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}

	if out.ActiveFavoriteList != "work" {
		t.Fatalf("active favorite list = %q, want work", out.ActiveFavoriteList)
	}
	if got := out.FavoriteLists["work"]; len(got) != 1 || got[0] != "/tmp/repo" {
		t.Fatalf("work favorites = %#v, want [/tmp/repo]", got)
	}
	if got := out.FavoriteLists["personal"]; len(got) != 1 || got[0] != "/tmp/other" {
		t.Fatalf("personal favorites = %#v, want [/tmp/other]", got)
	}
	var localRepo Repo
	var gerritRepo Repo
	for _, repo := range out.Repos {
		switch repo.Name {
		case "repo":
			localRepo = repo
		case "proj":
			gerritRepo = repo
		}
	}
	if localRepo.LastUpdated != "2026-07-02T10:11:12Z" {
		t.Fatalf("last updated = %q, want %q", localRepo.LastUpdated, "2026-07-02T10:11:12Z")
	}
	if got := gerritRepo.GerritProject; got != "team/proj" {
		t.Fatalf("gerrit project = %q, want %q", got, "team/proj")
	}
	if got := gerritRepo.RemoteURL; got != "ssh://alice@gerrit/team/proj" {
		t.Fatalf("remote url = %q, want %q", got, "ssh://alice@gerrit/team/proj")
	}
	if !out.Settings.ShowGitCommands {
		t.Fatal("show git commands should persist")
	}
	if !out.Settings.ShowRepoInfo {
		t.Fatal("show repo info should persist")
	}
	if out.Settings.GerritUsername != "alice" || out.Settings.GerritServer != "gerrit.example.com" || out.Settings.BaseGitDir != "/tmp/git" {
		t.Fatalf("gerrit settings mismatch: %+v", out.Settings)
	}
}

func TestSaveLoadPreservesDisabledRepoInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	s := &Store{path: path}

	in := State{
		Settings: Settings{
			ShowGitCommands: false,
			ShowRepoInfo:    false,
		},
	}
	if err := s.Save(in); err != nil {
		t.Fatalf("save state: %v", err)
	}

	out, err := s.Load()
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}

	if out.Settings.ShowRepoInfo {
		t.Fatal("show repo info should remain false after reload")
	}
}

func TestLoadDeduplicatesByGerritProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	data := []byte(`{
  "repos": [
    {"name":"proj","path":"/tmp/git/a","gerritProject":"team/proj"},
    {"name":"proj-copy","path":"/tmp/other","gerritProject":"team/proj"}
  ]
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	s := &Store{path: path}
	state, err := s.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if got := len(state.Repos); got != 1 {
		t.Fatalf("repo count = %d, want 1", got)
	}
	if got := state.Repos[0].GerritProject; got != "team/proj" {
		t.Fatalf("project = %q, want team/proj", got)
	}
}
