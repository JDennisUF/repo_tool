package ui

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

//go:embed themes.json
var themeFiles embed.FS

type themeConfig struct {
	ActiveTheme string                  `json:"activeTheme"`
	Themes      map[string]themePalette `json:"themes"`
}

type themePalette struct {
	Background  string `json:"background"`
	Foreground  string `json:"foreground"`
	Muted       string `json:"muted"`
	Border      string `json:"border"`
	BorderFocus string `json:"borderFocus"`
	Header      string `json:"header"`
	Accent      string `json:"accent"`
	Selection   string `json:"selection"`
	Success     string `json:"success"`
	Error       string `json:"error"`
	Warning     string `json:"warning"`
	Status      string `json:"status"`
	StatusText  string `json:"statusText"`
	Cursor      string `json:"cursor"`
	Input       string `json:"input"`
}

type themeSet struct {
	Themes map[string]themePalette
	Names  []string
	Active string
}

func loadThemeSet() themeSet {
	cfg := mergedThemeConfig()
	name := cfg.ActiveTheme
	if envName := os.Getenv("RT_THEME"); envName != "" {
		name = envName
	}
	if _, ok := cfg.Themes[name]; !ok {
		name = cfg.ActiveTheme
	}
	if _, ok := cfg.Themes[name]; !ok {
		name = "fallback"
		cfg.Themes[name] = fallbackTheme()
	}

	for themeName, palette := range cfg.Themes {
		cfg.Themes[themeName] = palette.withDefaults()
	}

	return themeSet{
		Themes: cfg.Themes,
		Names:  sortedThemeNames(cfg.Themes),
		Active: name,
	}
}

func mergedThemeConfig() themeConfig {
	cfg := defaultThemeConfig()
	if userCfg, err := readUserThemeConfig(); err == nil && len(userCfg.Themes) > 0 {
		if cfg.Themes == nil {
			cfg.Themes = map[string]themePalette{}
		}
		for name, palette := range userCfg.Themes {
			cfg.Themes[name] = palette
		}
		if userCfg.ActiveTheme != "" {
			cfg.ActiveTheme = userCfg.ActiveTheme
		}
	}
	if cfg.ActiveTheme == "" {
		cfg.ActiveTheme = "graphite"
	}
	return cfg
}

func saveActiveTheme(name string) error {
	cfg := mergedThemeConfig()
	cfg.ActiveTheme = name
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(configDir, "rt", "themes.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func defaultThemeConfig() themeConfig {
	data, err := themeFiles.ReadFile("themes.json")
	if err != nil {
		return themeConfig{ActiveTheme: "fallback", Themes: map[string]themePalette{"fallback": fallbackTheme()}}
	}
	var cfg themeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return themeConfig{ActiveTheme: "fallback", Themes: map[string]themePalette{"fallback": fallbackTheme()}}
	}
	return cfg
}

func sortedThemeNames(themes map[string]themePalette) []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readUserThemeConfig() (themeConfig, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return themeConfig{}, err
	}
	data, err := os.ReadFile(filepath.Join(configDir, "rt", "themes.json"))
	if err != nil {
		return themeConfig{}, err
	}
	var cfg themeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return themeConfig{}, err
	}
	return cfg, nil
}

func (p themePalette) withDefaults() themePalette {
	f := fallbackTheme()
	if p.Background == "" {
		p.Background = f.Background
	}
	if p.Foreground == "" {
		p.Foreground = f.Foreground
	}
	if p.Muted == "" {
		p.Muted = f.Muted
	}
	if p.Border == "" {
		p.Border = f.Border
	}
	if p.BorderFocus == "" {
		p.BorderFocus = f.BorderFocus
	}
	if p.Header == "" {
		p.Header = f.Header
	}
	if p.Accent == "" {
		p.Accent = f.Accent
	}
	if p.Selection == "" {
		p.Selection = f.Selection
	}
	if p.Success == "" {
		p.Success = f.Success
	}
	if p.Error == "" {
		p.Error = f.Error
	}
	if p.Warning == "" {
		p.Warning = f.Warning
	}
	if p.Status == "" {
		p.Status = f.Status
	}
	if p.StatusText == "" {
		p.StatusText = f.StatusText
	}
	if p.Cursor == "" {
		p.Cursor = f.Cursor
	}
	if p.Input == "" {
		p.Input = f.Input
	}
	return p
}

func fallbackTheme() themePalette {
	return themePalette{
		Background:  "#0B0F14",
		Foreground:  "#F3F4F6",
		Muted:       "#A7B0BE",
		Border:      "#4B5563",
		BorderFocus: "#60A5FA",
		Header:      "#FCD34D",
		Accent:      "#22D3EE",
		Selection:   "#FBBF24",
		Success:     "#34D399",
		Error:       "#FB7185",
		Warning:     "#F59E0B",
		Status:      "#111827",
		StatusText:  "#FFFFFF",
		Cursor:      "#60A5FA",
		Input:       "#C084FC",
	}
}
