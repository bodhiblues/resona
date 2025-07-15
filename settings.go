package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme represents a color theme for the application
type Theme struct {
	Name         string `json:"name"`
	Primary      string `json:"primary"`      // Main accent color
	Secondary    string `json:"secondary"`    // Secondary accent color
	Background   string `json:"background"`   // Background color
	Foreground   string `json:"foreground"`   // Text color
	Muted        string `json:"muted"`        // Muted text color
	Border       string `json:"border"`       // Border color
	Highlight    string `json:"highlight"`    // Highlight/selection color
	Success      string `json:"success"`      // Success color
	Warning      string `json:"warning"`      // Warning color
	Error        string `json:"error"`        // Error color
	GradientStart string `json:"gradient_start"` // Progress bar gradient start color
	GradientEnd   string `json:"gradient_end"`   // Progress bar gradient end color
}

// Settings holds all user preferences
type Settings struct {
	Theme     string `json:"theme"`      // Current theme name
	Volume    int    `json:"volume"`     // Volume level (0-100)
	AutoPlay  bool   `json:"auto_play"`  // Auto-play next track
	Crossfade bool   `json:"crossfade"`  // Crossfade between tracks
}

// SettingsManager manages user settings and themes
type SettingsManager struct {
	settings   Settings
	themes     map[string]Theme
	filePath   string
	themesPath string
}

// NewSettingsManager creates a new settings manager
func NewSettingsManager() (*SettingsManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}
	
	configDir := filepath.Join(homeDir, ".resona")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}
	
	settingsPath := filepath.Join(configDir, "settings.json")
	themesPath := filepath.Join(configDir, "themes")
	
	sm := &SettingsManager{
		settings: Settings{
			Theme:     "default",
			Volume:    80,
			AutoPlay:  true,
			Crossfade: false,
		},
		themes:     make(map[string]Theme),
		filePath:   settingsPath,
		themesPath: themesPath,
	}
	
	// Load default themes
	sm.loadDefaultThemes()
	
	// Create themes directory
	if err := os.MkdirAll(themesPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create themes directory: %w", err)
	}
	
	// Load settings and themes from disk
	if err := sm.LoadSettings(); err != nil {
		// If file doesn't exist, that's okay - we'll create it on first save
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load settings: %w", err)
		}
	}
	
	if err := sm.LoadThemes(); err != nil {
		// If loading fails, create default theme files
		if err := sm.CreateDefaultThemeFiles(); err != nil {
			return nil, fmt.Errorf("failed to create default themes: %w", err)
		}
		sm.LoadThemes()
	}
	
	return sm, nil
}

// loadDefaultThemes loads the built-in themes
func (sm *SettingsManager) loadDefaultThemes() {
	// Default theme (current colors)
	sm.themes["default"] = Theme{
		Name:          "Default",
		Primary:       "205", // Pink
		Secondary:     "147", // Light purple
		Background:    "235", // Dark gray
		Foreground:    "252", // Light gray
		Muted:         "240", // Medium gray
		Border:        "241", // Border gray
		Highlight:     "205", // Pink highlight
		Success:       "46",  // Green
		Warning:       "226", // Yellow
		Error:         "196", // Red
		GradientStart: "147", // Light purple
		GradientEnd:   "205", // Pink
	}
	
	// Dark theme
	sm.themes["dark"] = Theme{
		Name:          "Dark",
		Primary:       "39",  // Blue
		Secondary:     "33",  // Darker blue
		Background:    "232", // Very dark gray
		Foreground:    "255", // White
		Muted:         "244", // Medium gray
		Border:        "238", // Dark border
		Highlight:     "39",  // Blue highlight
		Success:       "40",  // Green
		Warning:       "220", // Yellow
		Error:         "160", // Red
		GradientStart: "33",  // Darker blue
		GradientEnd:   "39",  // Blue
	}
	
	// Light theme
	sm.themes["light"] = Theme{
		Name:          "Light",
		Primary:       "25",  // Blue
		Secondary:     "67",  // Medium blue
		Background:    "255", // White
		Foreground:    "0",   // Black
		Muted:         "240", // Gray
		Border:        "244", // Light border
		Highlight:     "25",  // Blue highlight
		Success:       "22",  // Green
		Warning:       "178", // Orange
		Error:         "124", // Red
		GradientStart: "67",  // Medium blue
		GradientEnd:   "25",  // Blue
	}
	
	// Cyberpunk theme
	sm.themes["cyberpunk"] = Theme{
		Name:          "Cyberpunk",
		Primary:       "51",  // Cyan
		Secondary:     "201", // Magenta
		Background:    "0",   // Black
		Foreground:    "51",  // Cyan
		Muted:         "240", // Gray
		Border:        "51",  // Cyan border
		Highlight:     "201", // Magenta highlight
		Success:       "46",  // Green
		Warning:       "226", // Yellow
		Error:         "196", // Red
		GradientStart: "51",  // Cyan
		GradientEnd:   "201", // Magenta
	}
	
	// Forest theme
	sm.themes["forest"] = Theme{
		Name:          "Forest",
		Primary:       "28",  // Green
		Secondary:     "34",  // Darker green
		Background:    "22",  // Dark green
		Foreground:    "150", // Light green
		Muted:         "240", // Gray
		Border:        "28",  // Green border
		Highlight:     "34",  // Dark green highlight
		Success:       "46",  // Bright green
		Warning:       "178", // Orange
		Error:         "124", // Red
		GradientStart: "34",  // Darker green
		GradientEnd:   "46",  // Bright green
	}
	
	// Sunset theme
	sm.themes["sunset"] = Theme{
		Name:          "Sunset",
		Primary:       "208", // Orange
		Secondary:     "196", // Red
		Background:    "52",  // Dark red
		Foreground:    "224", // Light orange
		Muted:         "240", // Gray
		Border:        "208", // Orange border
		Highlight:     "196", // Red highlight
		Success:       "46",  // Green
		Warning:       "226", // Yellow
		Error:         "160", // Dark red
		GradientStart: "208", // Orange
		GradientEnd:   "196", // Red
	}
}

// LoadSettings loads settings from persistent storage
func (sm *SettingsManager) LoadSettings() error {
	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		return err
	}
	
	return json.Unmarshal(data, &sm.settings)
}

// SaveSettings saves settings to persistent storage
func (sm *SettingsManager) SaveSettings() error {
	data, err := json.MarshalIndent(sm.settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}
	
	return os.WriteFile(sm.filePath, data, 0644)
}

// LoadThemes loads themes from individual JSON files
func (sm *SettingsManager) LoadThemes() error {
	files, err := os.ReadDir(sm.themesPath)
	if err != nil {
		return err
	}
	
	sm.themes = make(map[string]Theme)
	
	for _, file := range files {
		if filepath.Ext(file.Name()) == ".json" {
			themeName := strings.TrimSuffix(file.Name(), ".json")
			themeFile := filepath.Join(sm.themesPath, file.Name())
			
			data, err := os.ReadFile(themeFile)
			if err != nil {
				continue // Skip invalid files
			}
			
			var theme Theme
			if err := json.Unmarshal(data, &theme); err != nil {
				continue // Skip invalid JSON
			}
			
			sm.themes[themeName] = theme
		}
	}
	
	// If no themes were loaded, return an error
	if len(sm.themes) == 0 {
		return fmt.Errorf("no valid themes found")
	}
	
	return nil
}

// SaveTheme saves a single theme to its JSON file
func (sm *SettingsManager) SaveTheme(themeName string, theme Theme) error {
	themeFile := filepath.Join(sm.themesPath, themeName+".json")
	data, err := json.MarshalIndent(theme, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal theme: %w", err)
	}
	
	return os.WriteFile(themeFile, data, 0644)
}

// CreateDefaultThemeFiles creates default theme JSON files
func (sm *SettingsManager) CreateDefaultThemeFiles() error {
	// Load default themes into memory first
	sm.loadDefaultThemes()
	
	// Save each theme to its own file
	for themeName, theme := range sm.themes {
		if err := sm.SaveTheme(themeName, theme); err != nil {
			return fmt.Errorf("failed to save theme %s: %w", themeName, err)
		}
	}
	
	return nil
}

// GetSettings returns current settings
func (sm *SettingsManager) GetSettings() Settings {
	return sm.settings
}

// SetTheme sets the current theme
func (sm *SettingsManager) SetTheme(themeName string) error {
	if _, exists := sm.themes[themeName]; !exists {
		return fmt.Errorf("theme '%s' not found", themeName)
	}
	
	sm.settings.Theme = themeName
	return sm.SaveSettings()
}

// GetTheme returns the current theme
func (sm *SettingsManager) GetTheme() Theme {
	theme, exists := sm.themes[sm.settings.Theme]
	if !exists {
		// Fallback to default theme
		return sm.themes["default"]
	}
	return theme
}

// GetThemeNames returns all available theme names
func (sm *SettingsManager) GetThemeNames() []string {
	var names []string
	for name := range sm.themes {
		names = append(names, name)
	}
	return names
}

// GetThemeByName returns a theme by name
func (sm *SettingsManager) GetThemeByName(name string) (Theme, bool) {
	theme, exists := sm.themes[name]
	return theme, exists
}

// CreateThemeStyles creates lipgloss styles based on current theme
func (sm *SettingsManager) CreateThemeStyles() ThemeStyles {
	theme := sm.GetTheme()
	
	return ThemeStyles{
		Primary:    lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)),
		Secondary:  lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)),
		Background: lipgloss.NewStyle().Background(lipgloss.Color(theme.Background)),
		Foreground: lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground)),
		Muted:      lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)),
		Border:     lipgloss.NewStyle().BorderForeground(lipgloss.Color(theme.Border)),
		Highlight:  lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Highlight)),
		Success:    lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success)),
		Warning:    lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warning)),
		Error:      lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Error)),
	}
}

// ThemeStyles contains pre-configured lipgloss styles
type ThemeStyles struct {
	Primary    lipgloss.Style
	Secondary  lipgloss.Style
	Background lipgloss.Style
	Foreground lipgloss.Style
	Muted      lipgloss.Style
	Border     lipgloss.Style
	Highlight  lipgloss.Style
	Success    lipgloss.Style
	Warning    lipgloss.Style
	Error      lipgloss.Style
}