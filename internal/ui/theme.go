package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	RowFocusBg  string `json:"rowFocusBg"`
}

type themeSet struct {
	Themes map[string]themePalette
	Names  []string
	Active string
}

func loadThemeSet() themeSet {
	cfg := mergedThemeConfig()
	name := resolveThemeName(cfg.Themes, cfg.ActiveTheme)
	if envName := os.Getenv("RT_THEME"); envName != "" {
		name = resolveThemeName(cfg.Themes, envName)
	}
	if _, ok := cfg.Themes[name]; !ok {
		name = resolveThemeName(cfg.Themes, cfg.ActiveTheme)
	}
	if _, ok := cfg.Themes[name]; !ok {
		name = "Fallback"
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
	if userCfg, err := readUserThemeConfig(); err == nil {
		userCfg = normalizeUserThemeConfig(userCfg, cfg.Themes)
		_ = writeUserThemeConfig(userCfg)
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
		cfg.ActiveTheme = "Graphite"
	}
	return cfg
}

func saveActiveTheme(name string) error {
	baseCfg := defaultThemeConfig()
	userCfg, err := readUserThemeConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		userCfg = themeConfig{}
	}
	userCfg = normalizeUserThemeConfig(userCfg, baseCfg.Themes)
	mergedThemes := make(map[string]themePalette, len(baseCfg.Themes)+len(userCfg.Themes))
	for themeName, palette := range baseCfg.Themes {
		mergedThemes[themeName] = palette
	}
	for themeName, palette := range userCfg.Themes {
		mergedThemes[themeName] = palette
	}
	userCfg.ActiveTheme = resolveThemeName(mergedThemes, name)
	return writeUserThemeConfig(userCfg)
}

func writeUserThemeConfig(cfg themeConfig) error {
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
		return themeConfig{ActiveTheme: "Fallback", Themes: map[string]themePalette{"Fallback": fallbackTheme()}}
	}
	var cfg themeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return themeConfig{ActiveTheme: "Fallback", Themes: map[string]themePalette{"Fallback": fallbackTheme()}}
	}
	return cfg
}

func sortedThemeNames(themes map[string]themePalette) []string {
	names := make([]string, 0, len(themes))
	for name := range themes {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		li := strings.ToLower(names[i])
		lj := strings.ToLower(names[j])
		if li == lj {
			return names[i] < names[j]
		}
		return li < lj
	})
	return names
}

func resolveThemeName(themes map[string]themePalette, requested string) string {
	if requested == "" {
		return requested
	}
	if _, ok := themes[requested]; ok {
		return requested
	}

	aliases := map[string]string{
		"aurora":   "Aurora",
		"cobalt":   "Cobalt",
		"daylight": "Daylight",
		"ember":    "Ember",
		"graphite": "Graphite",
		"violet":   "Violet",
		"fallback": "Fallback",
	}
	if canonical, ok := aliases[strings.ToLower(requested)]; ok {
		if _, exists := themes[canonical]; exists {
			return canonical
		}
	}

	for name := range themes {
		if strings.EqualFold(name, requested) {
			return name
		}
	}
	return requested
}

func normalizeUserThemeConfig(cfg themeConfig, builtIns map[string]themePalette) themeConfig {
	normalized := themeConfig{
		ActiveTheme: resolveThemeName(builtIns, cfg.ActiveTheme),
		Themes:      map[string]themePalette{},
	}
	for name, palette := range cfg.Themes {
		canonical := resolveThemeName(builtIns, name)
		if _, isBuiltIn := builtIns[canonical]; isBuiltIn {
			continue
		}
		normalized.Themes[canonical] = palette
	}
	return normalized
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
	if p.RowFocusBg == "" {
		p.RowFocusBg = f.RowFocusBg
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
		RowFocusBg:  "#111827",
	}
}
