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
}

func TestSaveLoadPreservesFavoriteLists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "repos.json")
	s := &Store{path: path}

	in := State{
		Repos: []Repo{
			{Name: "repo", Path: "/tmp/repo", Selected: true, LastOp: "pull ok"},
		},
		FavoriteLists: map[string][]string{
			"work":    {"/tmp/repo", "/tmp/repo"},
			"personal": {"/tmp/other"},
		},
		ActiveFavoriteList: "work",
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
}
