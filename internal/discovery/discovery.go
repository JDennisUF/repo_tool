package discovery

import (
	"io/fs"
	"path/filepath"
	"sort"
)

func ScanGitRepos(root string) ([]string, error) {
	found := map[string]struct{}{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if !d.IsDir() {
			return nil
		}

		if d.Name() == ".git" {
			repoRoot := filepath.Dir(path)
			found[repoRoot] = struct{}{}
			return filepath.SkipDir
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	repos := make([]string, 0, len(found))
	for path := range found {
		repos = append(repos, path)
	}
	sort.Strings(repos)
	return repos, nil
}
