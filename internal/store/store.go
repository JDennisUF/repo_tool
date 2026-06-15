package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

const (
	appName    = "rt"
	configFile = "repos.json"
)

type Repo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Selected bool   `json:"selected"`
	LastOp   string `json:"lastOp,omitempty"`
}

type persistedState struct {
	Repos []Repo `json:"repos"`
}

type Store struct {
	path string
}

func New() (*Store, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(configDir, appName, configFile)}, nil
}

func (s *Store) Load() ([]Repo, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Repo{}, nil
		}
		return nil, err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	normalized := make([]Repo, 0, len(state.Repos))
	seen := map[string]struct{}{}
	for _, r := range state.Repos {
		if r.Path == "" {
			continue
		}
		cleanPath := filepath.Clean(r.Path)
		if _, exists := seen[cleanPath]; exists {
			continue
		}
		r.Path = cleanPath
		if r.Name == "" {
			r.Name = filepath.Base(cleanPath)
		}
		normalized = append(normalized, r)
		seen[cleanPath] = struct{}{}
	}

	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Name < normalized[j].Name
	})

	return normalized, nil
}

func (s *Store) Save(repos []Repo) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	state := persistedState{Repos: repos}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o644)
}
