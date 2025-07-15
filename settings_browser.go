package main

import (
	"fmt"
)

// SettingsBrowser handles the settings interface
type SettingsBrowser struct {
	settingsManager *SettingsManager
	libraryManager  *LibraryManager
	radioLibrary    *RadioLibrary
	currentView     string // "main", "themes", "confirm_clear_music", "confirm_clear_radio"
	selected        int
	viewport        viewport
	// Theme selection
	themeSelected   int
	themeNames      []string
	// Confirmation state
	confirmAction   string // "clear_music", "clear_radio"
	confirmSelected int    // 0=cancel, 1=confirm
}

// NewSettingsBrowser creates a new settings browser
func NewSettingsBrowser(settingsManager *SettingsManager, libraryManager *LibraryManager, radioLibrary *RadioLibrary) *SettingsBrowser {
	return &SettingsBrowser{
		settingsManager: settingsManager,
		libraryManager:  libraryManager,
		radioLibrary:    radioLibrary,
		currentView:     "main",
		selected:        0,
		viewport:        viewport{top: 0, height: 20},
		themeNames:      settingsManager.GetThemeNames(),
		confirmSelected: 0,
	}
}

// GetCurrentView returns the current view
func (sb *SettingsBrowser) GetCurrentView() string {
	return sb.currentView
}

// MoveUp moves selection up
func (sb *SettingsBrowser) MoveUp() {
	switch sb.currentView {
	case "main":
		if sb.selected > 0 {
			sb.selected--
		}
	case "themes":
		if sb.themeSelected > 0 {
			sb.themeSelected--
		}
	case "confirm_clear_music", "confirm_clear_radio":
		if sb.confirmSelected > 0 {
			sb.confirmSelected--
		}
	}
}

// MoveDown moves selection down
func (sb *SettingsBrowser) MoveDown() {
	switch sb.currentView {
	case "main":
		maxItems := 2 // Clear Music Library, Clear Radio Library, Color Themes
		if sb.selected < maxItems {
			sb.selected++
		}
	case "themes":
		if sb.themeSelected < len(sb.themeNames)-1 {
			sb.themeSelected++
		}
	case "confirm_clear_music", "confirm_clear_radio":
		if sb.confirmSelected < 1 {
			sb.confirmSelected++
		}
	}
}

// EnterSelected handles enter key press
func (sb *SettingsBrowser) EnterSelected() error {
	switch sb.currentView {
	case "main":
		switch sb.selected {
		case 0: // Clear Music Library
			sb.currentView = "confirm_clear_music"
			sb.confirmAction = "clear_music"
			sb.confirmSelected = 0
		case 1: // Clear Radio Library
			sb.currentView = "confirm_clear_radio"
			sb.confirmAction = "clear_radio"
			sb.confirmSelected = 0
		case 2: // Color Themes
			sb.currentView = "themes"
			// Set current theme as selected
			currentTheme := sb.settingsManager.GetSettings().Theme
			for i, name := range sb.themeNames {
				if name == currentTheme {
					sb.themeSelected = i
					break
				}
			}
		}
	case "themes":
		// Apply selected theme
		if sb.themeSelected < len(sb.themeNames) {
			selectedTheme := sb.themeNames[sb.themeSelected]
			err := sb.settingsManager.SetTheme(selectedTheme)
			if err != nil {
				return fmt.Errorf("failed to set theme: %w", err)
			}
		}
		sb.currentView = "main"
	case "confirm_clear_music":
		if sb.confirmSelected == 1 { // Confirm
			err := sb.libraryManager.ClearLibrary()
			if err != nil {
				return fmt.Errorf("failed to clear music library: %w", err)
			}
		}
		sb.currentView = "main"
	case "confirm_clear_radio":
		if sb.confirmSelected == 1 { // Confirm
			err := sb.radioLibrary.ClearLibrary()
			if err != nil {
				return fmt.Errorf("failed to clear radio library: %w", err)
			}
		}
		sb.currentView = "main"
	}
	return nil
}

// BackPressed handles back/escape key press
func (sb *SettingsBrowser) BackPressed() {
	switch sb.currentView {
	case "themes", "confirm_clear_music", "confirm_clear_radio":
		sb.currentView = "main"
	}
}

// GetSelected returns current selection
func (sb *SettingsBrowser) GetSelected() int {
	return sb.selected
}

// GetThemeSelected returns current theme selection
func (sb *SettingsBrowser) GetThemeSelected() int {
	return sb.themeSelected
}

// GetConfirmSelected returns current confirmation selection
func (sb *SettingsBrowser) GetConfirmSelected() int {
	return sb.confirmSelected
}

// GetThemeNames returns available theme names
func (sb *SettingsBrowser) GetThemeNames() []string {
	return sb.themeNames
}

// GetConfirmAction returns current confirmation action
func (sb *SettingsBrowser) GetConfirmAction() string {
	return sb.confirmAction
}

// RefreshThemes refreshes the theme list
func (sb *SettingsBrowser) RefreshThemes() {
	sb.themeNames = sb.settingsManager.GetThemeNames()
}