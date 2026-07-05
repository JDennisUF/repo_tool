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
	Name        string `json:"name"`
	Path        string `json:"path"`
	Selected    bool   `json:"selected"`
	LastOp      string `json:"lastOp,omitempty"`
	LastUpdated string `json:"lastUpdated,omitempty"`
}

type State struct {
	Repos              []Repo              `json:"repos"`
	FavoriteLists      map[string][]string `json:"favoriteLists,omitempty"`
	ActiveFavoriteList string              `json:"activeFavoriteList,omitempty"`
	Settings           Settings            `json:"settings,omitempty"`
}

type Settings struct {
	ShowGitCommands bool `json:"showGitCommands,omitempty"`
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

func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultState(), nil
		}
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}

	normalizedRepos := make([]Repo, 0, len(state.Repos))
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
		normalizedRepos = append(normalizedRepos, r)
		seen[cleanPath] = struct{}{}
	}

	sort.Slice(normalizedRepos, func(i, j int) bool {
		return normalizedRepos[i].Name < normalizedRepos[j].Name
	})

	state.Repos = normalizedRepos
	state.FavoriteLists = normalizeFavoriteLists(state.FavoriteLists)
	if state.ActiveFavoriteList == "" {
		state.ActiveFavoriteList = defaultFavoriteListName
	}
	if _, ok := state.FavoriteLists[state.ActiveFavoriteList]; !ok {
		state.FavoriteLists[state.ActiveFavoriteList] = []string{}
	}

	return state, nil
}

func (s *Store) Save(state State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o644)
}

const defaultFavoriteListName = "default"

func defaultState() State {
	return State{
		Repos:              []Repo{},
		FavoriteLists:      map[string][]string{defaultFavoriteListName: []string{}},
		ActiveFavoriteList: defaultFavoriteListName,
	}
}

func normalizeFavoriteLists(lists map[string][]string) map[string][]string {
	normalized := make(map[string][]string)
	if len(lists) == 0 {
		normalized[defaultFavoriteListName] = []string{}
		return normalized
	}

	for name, paths := range lists {
		cleanName := name
		if cleanName == "" {
			continue
		}
		seen := map[string]struct{}{}
		cleanPaths := make([]string, 0, len(paths))
		for _, path := range paths {
			if path == "" {
				continue
			}
			cleanPath := filepath.Clean(path)
			if _, ok := seen[cleanPath]; ok {
				continue
			}
			seen[cleanPath] = struct{}{}
			cleanPaths = append(cleanPaths, cleanPath)
		}
		sort.Strings(cleanPaths)
		normalized[cleanName] = cleanPaths
	}

	if len(normalized) == 0 {
		normalized[defaultFavoriteListName] = []string{}
	}

	return normalized
}
