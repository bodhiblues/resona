package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/barchart"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/20, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type model struct {
	currentView       string
	width             int
	height            int
	playing           string
	playingSong       *Song
	playingStation    *RadioStation
	selected          int
	audioPlayer       *AudioPlayer
	folderBrowser     *FolderBrowser
	libraryManager    *LibraryManager
	libraryBrowser    *LibraryBrowser
	radioLibrary      *RadioLibrary
	radioBrowser      *RadioBrowser
	settingsManager   *SettingsManager
	settingsBrowser   *SettingsBrowser
	nowPlayingFocused bool
	controlSelected   int // 0=prev, 1=play/pause, 2=next, 3=stop
	// Search functionality
	searchMode        bool
	searchQuery       string
	searchResults     []Song
	searchSelected    int
	// Main content viewport
	contentViewport   viewport
	contentLines      []string
	// Playlist/queue management
	currentPlaylist   []Song
	currentTrackIndex int
	// Radio spinner and timer
	spinner           spinner.Model
	radioStartTime    time.Time
	radioPausedTime   time.Duration
	radioWasPaused    bool
	// Visualizer
	visualizer        barchart.Model
}


func initialModel() model {
	audioPlayer, err := NewAudioPlayer()
	if err != nil {
		fmt.Printf("Error initializing audio player: %v\n", err)
		os.Exit(1)
	}
	
	folderBrowser, err := NewFolderBrowser()
	if err != nil {
		fmt.Printf("Error initializing folder browser: %v\n", err)
		os.Exit(1)
	}
	
	libraryManager, err := NewLibraryManager()
	if err != nil {
		fmt.Printf("Error initializing library manager: %v\n", err)
		os.Exit(1)
	}
	
	libraryBrowser := NewLibraryBrowser(libraryManager)
	
	radioLibrary, err := NewRadioLibrary()
	if err != nil {
		fmt.Printf("Error initializing radio library: %v\n", err)
		os.Exit(1)
	}
	
	radioBrowser := NewRadioBrowser(radioLibrary)
	
	settingsManager, err := NewSettingsManager()
	if err != nil {
		fmt.Printf("Error initializing settings manager: %v\n", err)
		os.Exit(1)
	}
	
	settingsBrowser := NewSettingsBrowser(settingsManager, libraryManager, radioLibrary)
	
	// Initialize spinner
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(settingsManager.GetTheme().Primary))
	
	// Initialize visualizer
	visualizer := createVisualizer(settingsManager.GetTheme())
	
	m := model{
		currentView:       "library",
		playing:           "",
		playingSong:       nil,
		playingStation:    nil,
		selected:          0,
		audioPlayer:       audioPlayer,
		folderBrowser:     folderBrowser,
		libraryManager:    libraryManager,
		libraryBrowser:    libraryBrowser,
		radioLibrary:      radioLibrary,
		radioBrowser:      radioBrowser,
		settingsManager:   settingsManager,
		settingsBrowser:   settingsBrowser,
		nowPlayingFocused: false,
		controlSelected:   1, // Start with play/pause selected
		spinner:           s,
		visualizer:        visualizer,
	}
	
	// Reset viewport to ensure proper initial display
	m.libraryBrowser.ResetViewport()
	
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
		
	case tickMsg:
		// Check if current track has finished and auto-play next
		if m.isTrackFinished() {
			if m.playNextTrack() {
				return m, tickCmd()
			} else {
				// No more tracks, stop playing
				m.playing = ""
				m.playingSong = nil
				return m, nil
			}
		}
		
		// Continue ticking if there's a song loaded (playing or paused) or radio station playing
		// This ensures we can detect when a track finishes and auto-advance, and keeps spinner/timer updated
		if m.playingSong != nil || m.playingStation != nil {
			return m, tickCmd()
		}
		return m, nil
		
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		
		// Calculate proper content height
		headerHeight := 1
		tabsHeight := 1
		statusHeight := 1
		nowPlayingHeight := 0
		if m.playingSong != nil || m.playingStation != nil {
			// Calculate actual now-playing height based on content
			nowPlayingHeight = m.calculateNowPlayingHeight()
		}
		contentHeight := msg.Height - headerHeight - tabsHeight - statusHeight - nowPlayingHeight - 3
		if contentHeight < 5 {
			contentHeight = 5
		}
		
		// Update sub-component viewport heights
		m.folderBrowser.SetViewportHeight(contentHeight)
		m.libraryBrowser.SetViewportHeight(contentHeight)
		
		return m, nil

	case tea.KeyMsg:
		keyStr := msg.String()
		
		// Handle radio input mode first (prevent function keys from working during text input)
		if m.currentView == "radio" && m.radioBrowser.IsInputMode() {
			switch keyStr {
			case "enter":
				m.radioBrowser.FinishInput()
				return m, nil
			case "esc":
				m.radioBrowser.CancelInput()
				return m, nil
			case "backspace":
				m.radioBrowser.RemoveInputChar()
				return m, nil
			default:
				// Add character to input
				if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
					m.radioBrowser.AddInputChar(rune(keyStr[0]))
				}
				return m, nil
			}
		}
		
		// Handle search mode
		if m.searchMode {
			switch keyStr {
			case "esc":
				m.searchMode = false
				m.searchQuery = ""
				m.searchResults = []Song{}
				return m, nil
			case "enter":
				if len(m.searchResults) > 0 && m.searchSelected < len(m.searchResults) {
					// Create playlist from search results
					playlist := make([]Song, len(m.searchResults))
					copy(playlist, m.searchResults)
					
					m.setPlaylist(playlist, m.searchSelected)
					if m.playCurrentTrack() {
						m.searchMode = false
						m.searchQuery = ""
						m.searchResults = []Song{}
						return m, tickCmd()
					}
				}
				return m, nil
			case "up":
				if m.searchSelected > 0 {
					m.searchSelected--
				}
				return m, nil
			case "down":
				if m.searchSelected < len(m.searchResults)-1 {
					m.searchSelected++
				}
				return m, nil
			case "backspace":
				if len(m.searchQuery) > 0 {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
					m.performSearch(m.searchQuery)
				}
				return m, nil
			default:
				// Add character to search query
				if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] <= 126 {
					m.searchQuery += keyStr
					m.performSearch(m.searchQuery)
				}
				return m, nil
			}
		}
		
		switch keyStr {
		case "/":
			// Enter search mode
			m.searchMode = true
			m.searchQuery = ""
			m.searchResults = []Song{}
			m.searchSelected = 0
			return m, nil
		case "space", " ":
			if m.nowPlayingFocused {
				// If in now playing controls, activate selected control
				return m.activateControl()
			} else {
				// Normal play/pause toggle
				wasPaused := m.audioPlayer.IsPaused()
				m.audioPlayer.TogglePause()
				
				// Handle radio pause tracking
				if m.playingStation != nil {
					if wasPaused {
						// Resuming from pause - update pause tracking
						if m.radioWasPaused {
							m.radioPausedTime += time.Since(m.radioStartTime)
						}
						m.radioStartTime = time.Now()
					} else {
						// Pausing - mark as paused
						m.radioWasPaused = true
					}
				}
				
				if m.audioPlayer.IsPlaying() {
					return m, tickCmd() // Start ticking when resuming
				}
				return m, nil
			}
		case "q", "ctrl+c":
			m.audioPlayer.Stop()
			return m, tea.Quit
		case "s":
			if m.currentView == "radio" && m.radioBrowser.GetCurrentView() == "add" {
				if err := m.radioBrowser.SaveStation(); err == nil {
					// Station saved successfully
				}
			} else if m.currentView == "radio" && m.radioBrowser.GetCurrentView() == "quickadd" {
				if err := m.radioBrowser.SaveQuickStation(); err == nil {
					// Quick station saved, now play it
					stations := m.radioBrowser.GetStations()
					if len(stations) > 0 {
						lastStation := &stations[len(stations)-1]
						if err := m.audioPlayer.Play(lastStation.StreamURL); err == nil {
							m.playing = lastStation.Name
							m.playingSong = nil
							m.playingStation = lastStation
							return m, tickCmd()
						}
					}
				}
			} else {
				m.audioPlayer.Stop()
				m.playing = ""
				m.playingSong = nil
				m.playingStation = nil
				m.radioStartTime = time.Time{}
				m.radioPausedTime = 0
				m.radioWasPaused = false
				m.nowPlayingFocused = false
			}
			return m, nil
		case "p":
			if m.currentView == "radio" && m.radioBrowser.GetCurrentView() == "quickadd" {
				if station, err := m.radioBrowser.PlayQuickStation(); err == nil {
					if err := m.audioPlayer.Play(station.StreamURL); err == nil {
						m.playing = station.Name
						m.playingSong = nil
						m.playingStation = station
						m.radioStartTime = time.Now()
						m.radioPausedTime = 0
						m.radioWasPaused = false
						return m, tickCmd()
					}
				}
			}
			return m, nil
		case "f":
			if m.currentView == "library" {
				m.currentView = "folder"
				m.selected = 0
			} else if m.currentView == "folder" {
				m.currentView = "radio"
				m.selected = 0
			} else if m.currentView == "radio" {
				m.currentView = "settings"
				m.selected = 0
			} else if m.currentView == "settings" {
				m.currentView = "library"
				m.selected = 0
				m.libraryBrowser.ResetViewport()
			}
			return m, nil
		case "a":
			if m.currentView == "folder" {
				if selected := m.folderBrowser.GetSelected(); selected != "" {
					if m.folderBrowser.IsDirectory(selected) {
						// Add folder to library
						if err := m.libraryManager.AddFolder(selected); err == nil {
							m.libraryBrowser.Refresh()
							m.currentView = "library"
							m.selected = 0
							m.libraryBrowser.ResetViewport()
						}
					}
				}
			} else if m.currentView == "radio" {
				if m.radioBrowser.GetCurrentView() == "quickadd" {
					m.radioBrowser.showAddForm()
				} else {
					m.radioBrowser.showAddForm()
				}
			}
			return m, nil
		case "r":
			if m.currentView == "library" {
				// Rescan library
				m.libraryManager.RescanLibrary()
				m.libraryBrowser.Refresh()
			}
			return m, nil
		case "tab":
			if m.currentView == "library" {
				if m.nowPlayingFocused {
					// Switch from now playing to library
					m.nowPlayingFocused = false
				} else {
					m.libraryBrowser.SwitchPane()
				}
			}
			return m, nil
		case "shift+tab":
			if m.currentView == "library" && m.playingSong != nil && !m.nowPlayingFocused {
				// Switch to now playing controls
				m.nowPlayingFocused = true
			}
			return m, nil
		case "up", "k":
			if m.nowPlayingFocused {
				// Exit now playing focus
				m.nowPlayingFocused = false
			} else if m.currentView == "library" {
				m.libraryBrowser.MoveUp()
			} else if m.currentView == "folder" {
				m.folderBrowser.MoveUp()
			} else if m.currentView == "radio" {
				m.radioBrowser.MoveUp()
			} else if m.currentView == "settings" {
				m.settingsBrowser.MoveUp()
			}
			return m, nil
		case "down", "j":
			if m.nowPlayingFocused {
				// Stay in now playing, no down movement
			} else if m.currentView == "library" {
				m.libraryBrowser.MoveDown()
			} else if m.currentView == "folder" {
				m.folderBrowser.MoveDown()
			} else if m.currentView == "radio" {
				m.radioBrowser.MoveDown()
			} else if m.currentView == "settings" {
				m.settingsBrowser.MoveDown()
			}
			return m, nil
		case "left", "h":
			if m.nowPlayingFocused {
				if m.controlSelected > 0 {
					m.controlSelected--
				}
			}
			return m, nil
		case "right", "l":
			if m.nowPlayingFocused {
				if m.controlSelected < 3 {
					m.controlSelected++
				}
			}
			return m, nil
		case "enter":
			if m.nowPlayingFocused {
				return m.activateControl()
			} else if m.currentView == "library" {
				if song := m.libraryBrowser.EnterSelected(); song != nil {
					// Create playlist based on current context
					playlist := m.createPlaylistFromContext(song)
					songIndex := m.findSongInPlaylist(playlist, song)
					
					if songIndex >= 0 {
						m.setPlaylist(playlist, songIndex)
						if m.playCurrentTrack() {
							return m, tickCmd()
						}
					}
				}
			} else if m.currentView == "folder" {
				if selected := m.folderBrowser.GetSelected(); selected != "" {
					if m.folderBrowser.IsDirectory(selected) {
						m.folderBrowser.EnterDirectory(selected)
					}
				}
			} else if m.currentView == "radio" {
				return m.handleRadioEnter()
			} else if m.currentView == "settings" {
				if err := m.settingsBrowser.EnterSelected(); err != nil {
					// Handle error - could add error display
				}
				// Update spinner color when theme changes
				theme := m.settingsManager.GetTheme()
				m.spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
			}
			return m, nil
		case "esc":
			if m.currentView == "radio" {
				if m.radioBrowser.GetCurrentView() == "add" {
					if m.radioBrowser.IsInputMode() {
						m.radioBrowser.CancelInput()
					} else {
						m.radioBrowser.CancelAdd()
					}
				} else if m.radioBrowser.GetCurrentView() == "quickadd" {
					if m.radioBrowser.IsInputMode() {
						m.radioBrowser.CancelInput()
					} else {
						m.radioBrowser.CancelQuickAdd()
					}
				}
			} else if m.currentView == "settings" {
				m.settingsBrowser.BackPressed()
			}
			return m, nil
		case "backspace":
			if m.currentView == "library" {
				m.libraryBrowser.GoBack()
			} else if m.currentView == "folder" {
				m.folderBrowser.GoBack()
			} else if m.currentView == "radio" && m.radioBrowser.IsInputMode() {
				m.radioBrowser.RemoveInputChar()
			}
			return m, nil
		default:
			return m, nil
		}
	}
	return m, nil
}

func (m model) View() string {
	// Calculate available heights
	headerHeight := 1
	tabsHeight := 1
	statusHeight := 1
	nowPlayingHeight := 0
	if m.playingSong != nil {
		nowPlayingHeight = 10 // Updated height for now playing box with spacing
	}
	
	// Calculate content viewport height
	availableHeight := m.height - headerHeight - tabsHeight - statusHeight - nowPlayingHeight - 3 // 3 for spacing
	if availableHeight < 5 {
		availableHeight = 5
	}
	
	// Render fixed components
	theme := m.settingsManager.GetTheme()
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(2)

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		PaddingLeft(2)

	headerText := "🎵 Resona - Terminal Music Player"
	if m.currentView == "library" {
		folderCount := m.libraryManager.GetFolderCount()
		songCount := m.libraryManager.GetSongCount()
		breadcrumb := m.libraryBrowser.GetBreadcrumb()
		if len(breadcrumb) > 0 {
			headerText += fmt.Sprintf(" - Library: %s", strings.Join(breadcrumb, " > "))
		} else {
			headerText += fmt.Sprintf(" - Library: %d folders, %d songs", folderCount, songCount)
		}
	} else {
		headerText += fmt.Sprintf(" - Browse: %s", m.folderBrowser.GetCurrentPath())
	}
	header := headerStyle.Render(headerText)
	tabs := m.renderTabs()
	
	// Render main content with viewport
	content := m.renderMainContent(availableHeight)
	
	// Render status
	playStatus := "⏹️  Stopped"
	if m.audioPlayer.IsPlaying() {
		playStatus = "▶️  Playing"
	} else if m.audioPlayer.IsPaused() {
		playStatus = "⏸️  Paused"
	}
	
	var controlsText string
	if m.nowPlayingFocused {
		controlsText = "←/→ navigate controls, enter/space to activate, ↑ to exit controls, / to search, q to quit"
	} else if m.currentView == "library" {
		if m.libraryBrowser.GetCurrentPane() == "categories" {
			controlsText = "↑/↓ navigate categories, tab to switch panes, enter/space to pause, shift+tab for controls, / to search, f for folder browser, r to rescan, q to quit"
		} else {
			controlsText = "↑/↓ navigate, enter to select/play, backspace to go back, tab to switch panes, shift+tab for controls, / to search, f for folder browser, q to quit"
		}
	} else if m.currentView == "radio" {
		if m.radioBrowser.GetCurrentView() == "add" {
			if m.radioBrowser.IsInputMode() {
				controlsText = "Type to input, enter to save, escape to cancel"
			} else {
				controlsText = "↑/↓ navigate fields, enter to edit, 's' to save station, escape to cancel"
			}
		} else if m.radioBrowser.GetCurrentView() == "quickadd" {
			if m.radioBrowser.IsInputMode() {
				controlsText = "Type to input, enter to save, escape to cancel"
			} else {
				controlsText = "↑/↓ navigate, enter to edit, 'p' to play now, 's' to save & play, 'a' for full form, escape to cancel"
			}
		} else {
			controlsText = "↑/↓ navigate, enter to play station, 'a' to add station, f to switch view, q to quit"
		}
	} else {
		controlsText = "↑/↓ navigate, enter to open, a to add folder to library, backspace to go back, / to search, f for library, q to quit"
	}
	
	status := statusStyle.Render(fmt.Sprintf("Playing: %s | %s | %s", 
		m.playing, 
		playStatus,
		controlsText))

	// Build the main view
	var viewParts []string
	viewParts = append(viewParts, header)
	viewParts = append(viewParts, tabs)
	viewParts = append(viewParts, "")
	viewParts = append(viewParts, content)
	
	// Add now playing view if a song or radio station is playing
	if m.playingSong != nil || m.playingStation != nil {
		viewParts = append(viewParts, "")
		viewParts = append(viewParts, m.renderNowPlaying())
	}
	
	viewParts = append(viewParts, "")
	viewParts = append(viewParts, status)

	mainView := lipgloss.JoinVertical(lipgloss.Left, viewParts...)
	
	// Overlay search if active
	if m.searchMode {
		searchOverlay := m.renderSearch()
		return searchOverlay
	}
	
	return mainView
}

func (m model) renderTabs() string {
	// Get theme styles
	theme := m.settingsManager.GetTheme()
	
	// Define tab styles using theme colors
	activeTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		Background(lipgloss.Color(theme.Background)).
		Padding(0, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Bold(true)
	
	inactiveTabStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		Background(lipgloss.Color(theme.Background)).
		Padding(0, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border))
	
	// Create tabs
	libraryTab := "Library"
	filesTab := "Files"
	radioTab := "Radio"
	settingsTab := "Settings"
	
	if m.currentView == "library" {
		libraryTab = activeTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "folder" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = activeTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "radio" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = activeTabStyle.Render(radioTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "settings" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		settingsTab = activeTabStyle.Render(settingsTab)
	}
	
	// Join tabs with spacing
	tabsLine := lipgloss.JoinHorizontal(lipgloss.Top, libraryTab, " ", filesTab, " ", radioTab, " ", settingsTab)
	
	return tabsLine
}

func (m model) renderMainContent(availableHeight int) string {
	// Get the raw content
	var rawContent string
	switch m.currentView {
	case "library":
		rawContent = m.renderLibrary()
	case "folder":
		rawContent = m.renderFolderBrowser()
	case "radio":
		rawContent = m.renderRadio()
	case "settings":
		rawContent = m.renderSettings()
	default:
		rawContent = "Unknown view"
	}
	
	// Split content into lines
	contentLines := strings.Split(rawContent, "\n")
	
	// If content fits within available height, return as is
	if len(contentLines) <= availableHeight {
		return rawContent
	}
	
	// For viewport scrolling, we'll let the sub-components handle their own scrolling
	// since they already have viewport logic implemented
	// Just truncate if still too long
	if len(contentLines) > availableHeight {
		contentLines = contentLines[:availableHeight]
	}
	
	return strings.Join(contentLines, "\n")
}

func createGradientStyle(selected bool, width int, theme Theme) lipgloss.Style {
	if !selected {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Foreground)).
			PaddingLeft(1)
	}
	
	// Create gradient colors using theme colors
	gradient := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.Primary)).
		Foreground(lipgloss.Color(theme.Background)).
		Bold(true).
		PaddingLeft(1).
		Width(width)
	
	return gradient
}

// Color blending functions for gradient progress bar
type RGB struct {
	R, G, B float64
}

func hexToRGB(hex string) RGB {
	if len(hex) != 7 || hex[0] != '#' {
		return RGB{0, 0, 0}
	}
	
	r, _ := strconv.ParseInt(hex[1:3], 16, 64)
	g, _ := strconv.ParseInt(hex[3:5], 16, 64)
	b, _ := strconv.ParseInt(hex[5:7], 16, 64)
	
	return RGB{float64(r), float64(g), float64(b)}
}

func rgbToHex(rgb RGB) string {
	return fmt.Sprintf("#%02x%02x%02x", int(rgb.R), int(rgb.G), int(rgb.B))
}

func blendColors(colorA, colorB RGB, ratio float64) RGB {
	return RGB{
		R: colorA.R + (colorB.R-colorA.R)*ratio,
		G: colorA.G + (colorB.G-colorA.G)*ratio,
		B: colorA.B + (colorB.B-colorA.B)*ratio,
	}
}

// colorCodeToRGB converts ANSI color codes to RGB values
func colorCodeToRGB(colorCode string) RGB {
	// Basic ANSI color mapping to approximate RGB values
	colorMap := map[string]RGB{
		"0":   {0, 0, 0},         // Black
		"1":   {128, 0, 0},       // Dark Red
		"2":   {0, 128, 0},       // Dark Green
		"3":   {128, 128, 0},     // Dark Yellow
		"4":   {0, 0, 128},       // Dark Blue
		"5":   {128, 0, 128},     // Dark Magenta
		"6":   {0, 128, 128},     // Dark Cyan
		"7":   {192, 192, 192},   // Light Gray
		"8":   {128, 128, 128},   // Dark Gray
		"9":   {255, 0, 0},       // Red
		"10":  {0, 255, 0},       // Green
		"11":  {255, 255, 0},     // Yellow
		"12":  {0, 0, 255},       // Blue
		"13":  {255, 0, 255},     // Magenta
		"14":  {0, 255, 255},     // Cyan
		"15":  {255, 255, 255},   // White
		"22":  {0, 95, 0},        // Dark Green
		"25":  {0, 95, 175},      // Blue
		"28":  {0, 135, 0},       // Green
		"33":  {0, 135, 175},     // Darker blue
		"34":  {0, 175, 0},       // Darker green
		"39":  {0, 175, 255},     // Blue
		"40":  {0, 215, 0},       // Green
		"46":  {0, 255, 0},       // Bright green
		"51":  {0, 255, 255},     // Cyan
		"52":  {95, 0, 0},        // Dark red
		"67":  {95, 135, 215},    // Medium blue
		"124": {175, 0, 0},       // Red
		"147": {175, 135, 255},   // Light purple
		"150": {175, 255, 135},   // Light green
		"160": {215, 0, 0},       // Dark red
		"178": {215, 175, 0},     // Orange
		"196": {255, 0, 0},       // Red
		"201": {255, 0, 255},     // Magenta
		"205": {255, 95, 255},    // Pink
		"208": {255, 135, 0},     // Orange
		"220": {255, 215, 0},     // Yellow
		"224": {255, 215, 135},   // Light orange
		"226": {255, 255, 0},     // Yellow
		"232": {8, 8, 8},         // Very dark gray
		"235": {38, 38, 38},      // Dark gray
		"238": {68, 68, 68},      // Dark border
		"240": {88, 88, 88},      // Medium gray
		"241": {98, 98, 98},      // Border gray
		"244": {128, 128, 128},   // Medium gray
		"252": {208, 208, 208},   // Light gray
		"255": {255, 255, 255},   // White
	}
	
	if rgb, exists := colorMap[colorCode]; exists {
		return rgb
	}
	
	// Default to a neutral gray if color code not found
	return RGB{128, 128, 128}
}

func makeProgressGradient(steps int, theme Theme) []lipgloss.Style {
	// Convert theme colors to RGB
	colorA := colorCodeToRGB(theme.GradientStart)
	colorB := colorCodeToRGB(theme.GradientEnd)
	
	var styles []lipgloss.Style
	for i := 0; i < steps; i++ {
		ratio := float64(i) / float64(steps-1)
		blended := blendColors(colorA, colorB, ratio)
		color := rgbToHex(blended)
		
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Bold(true)
		styles = append(styles, style)
	}
	
	return styles
}

func (m model) calculateNowPlayingHeight() int {
	if m.playingSong == nil && m.playingStation == nil {
		return 0
	}
	
	// Calculate the actual height of the now-playing section
	// Structure: title + empty + info + empty + progress + empty + controls
	// Plus border and padding
	contentLines := 7 // title, empty, info, empty, progress, empty, controls
	borderAndPadding := 4 // top/bottom border + padding
	
	return contentLines + borderAndPadding
}

func (m model) renderNowPlaying() string {
	if m.playingSong == nil && m.playingStation == nil {
		return ""
	}

	// Get theme styles
	theme := m.settingsManager.GetTheme()
	
	// Create styles using theme colors
	nowPlayingStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		PaddingLeft(2)

	// Song or radio station info
	var songInfo string
	if m.playingSong != nil {
		songInfo = fmt.Sprintf("♪ %s", m.playingSong.Title)
		if m.playingSong.Artist != "Unknown Artist" {
			songInfo += fmt.Sprintf(" - %s", m.playingSong.Artist)
		}
		if m.playingSong.Album != "Unknown Album" {
			songInfo += fmt.Sprintf(" (%s)", m.playingSong.Album)
		}
	} else if m.playingStation != nil {
		songInfo = fmt.Sprintf("📻 %s", m.playingStation.Name)
		if m.playingStation.Genre != "" {
			songInfo += fmt.Sprintf(" - %s", m.playingStation.Genre)
		}
		if m.playingStation.Country != "" {
			songInfo += fmt.Sprintf(" (%s)", m.playingStation.Country)
		}
	}
	
	// Control buttons - simpler, cleaner approach
	controls := []string{"⏮️", "⏯️", "⏭️", "⏹️"}
	controlLabels := []string{"Prev", "Play", "Next", "Stop"}
	
	// Adjust play/pause button based on current state
	if m.audioPlayer.IsPlaying() {
		controls[1] = "⏸️"
		controlLabels[1] = "Pause"
	} else {
		controls[1] = "▶️"
		controlLabels[1] = "Play"
	}
	
	var controlParts []string
	for i, control := range controls {
		// Create different styles for different states
		var buttonStyle lipgloss.Style
		
		if m.nowPlayingFocused && i == m.controlSelected {
			// Focused and selected - bright highlight
			buttonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Background)).
				Background(lipgloss.Color(theme.Primary)).
				Bold(true).
				Padding(0, 1)
		} else if i == m.controlSelected {
			// Selected but not focused - subtle highlight
			buttonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Primary)).
				Background(lipgloss.Color(theme.Muted)).
				Bold(true).
				Padding(0, 1)
		} else {
			// Normal state
			buttonStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Foreground)).
				Bold(true).
				Padding(0, 1)
		}
		
		// Create button content
		buttonContent := fmt.Sprintf("%s %s", control, controlLabels[i])
		controlParts = append(controlParts, buttonStyle.Render(buttonContent))
	}
	
	controlsLine := strings.Join(controlParts, "  ")
	
	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 40
	}
	
	// Calculate the exact inner content width
	innerWidth := totalWidth - 4 // Account for "│ " and " │"
	
	// Progress bar
	progressBar := m.renderProgressBar(innerWidth)
	
	// Create a unified style that handles the entire box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(totalWidth - 2).
		Padding(1, 2)
	
	// Build content without borders - let lipgloss handle the box
	var contentLines []string
	
	// Now Playing title
	contentLines = append(contentLines, nowPlayingStyle.Render("Now Playing"))
	contentLines = append(contentLines, "")
	
	// Song info
	contentLines = append(contentLines, nowPlayingStyle.Render(songInfo))
	contentLines = append(contentLines, "")
	
	// Progress bar
	contentLines = append(contentLines, progressBar)
	contentLines = append(contentLines, "")
	
	// Controls
	contentLines = append(contentLines, controlsLine)
	
	// Join content and apply box style
	content := strings.Join(contentLines, "\n")
	boxContent := boxStyle.Render(content)
	
	return boxContent
}

func (m model) renderProgressBar(width int) string {
	if m.playingSong == nil && m.playingStation == nil {
		return ""
	}
	
	// Radio stations show spinner and listening time
	if m.playingStation != nil {
		theme := m.settingsManager.GetTheme()
		
		// Check if radio is paused
		if m.audioPlayer.IsPaused() {
			// Radio is paused - show paused state
			displayStr := "⏸️  Paused"
			return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render(displayStr)
		}
		
		// Calculate listening time (accounting for pause time)
		var listeningTime time.Duration
		if m.radioWasPaused {
			listeningTime = m.radioPausedTime + time.Since(m.radioStartTime)
		} else {
			listeningTime = time.Since(m.radioStartTime)
		}
		timeStr := formatDurationFromSeconds(listeningTime.Seconds())
		
		// Create spinner and timer display
		spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
		spinnerStr := spinnerStyle.Render(m.spinner.View())
		
		textStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground))
		displayStr := fmt.Sprintf("%s %s %s", spinnerStr, textStyle.Render("Streaming..."), textStyle.Render(timeStr))
		
		return displayStr
	}
	
	// Get current position and duration
	position := m.audioPlayer.GetPosition()
	duration := m.audioPlayer.GetDuration()
	progress := m.audioPlayer.GetProgress()
	
	// Format time strings
	positionStr := formatDurationFromSeconds(position)
	durationStr := m.playingSong.Duration
	if durationStr == "0:00" {
		durationStr = formatDurationFromSeconds(duration)
	}
	
	timeStr := positionStr + " / " + durationStr
	
	// Reserve space for visualizer and time display
	visualizerWidth := 24
	timeWidth := len(timeStr)
	reservedSpace := visualizerWidth + timeWidth + 4 // 4 for spacing between components
	
	// Calculate progress bar width (remaining space after reserving for visualizer and time)
	barWidth := width - reservedSpace
	if barWidth < 10 {
		barWidth = 10
	}
	
	// Increase resolution for smoother updates - use a minimum width for better granularity
	if barWidth < 50 {
		barWidth = 50
	}
	
	// Create gradient progress bar with higher resolution simulation
	// Use a virtual resolution that's 8x higher than actual characters
	virtualWidth := barWidth * 8
	virtualProgress := int(float64(virtualWidth) * progress)
	
	// Convert virtual progress back to actual character positions
	actualFilledWidth := virtualProgress / 8
	remainder := virtualProgress % 8
	
	if actualFilledWidth > barWidth {
		actualFilledWidth = barWidth
		remainder = 0
	}
	
	// Generate gradient styles for the progress bar
	theme := m.settingsManager.GetTheme()
	gradientStyles := makeProgressGradient(barWidth, theme)
	
	// Create background style using shaded characters
	backgroundStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted))
	
	// Build the progress bar
	var barParts []string
	for i := 0; i < barWidth; i++ {
		if i < actualFilledWidth {
			// Full filled character
			if i < len(gradientStyles) {
				barParts = append(barParts, gradientStyles[i].Render("█"))
			} else {
				barParts = append(barParts, gradientStyles[len(gradientStyles)-1].Render("█"))
			}
		} else if i == actualFilledWidth && remainder > 0 {
			// This character position has some virtual progress
			// Use a partial character based on the remainder
			var partialChar string
			if remainder == 1 {
				partialChar = "▏"
			} else if remainder == 2 {
				partialChar = "▎"
			} else if remainder == 3 {
				partialChar = "▍"
			} else if remainder == 4 {
				partialChar = "▌"
			} else if remainder == 5 {
				partialChar = "▋"
			} else if remainder == 6 {
				partialChar = "▊"
			} else if remainder == 7 {
				partialChar = "▉"
			} else {
				partialChar = "█"
			}
			
			var partialStyle lipgloss.Style
			if i < len(gradientStyles) {
				partialStyle = gradientStyles[i]
			} else {
				partialStyle = gradientStyles[len(gradientStyles)-1]
			}
			
			barParts = append(barParts, partialStyle.Render(partialChar))
		} else {
			// Background character
			barParts = append(barParts, backgroundStyle.Render("░"))
		}
	}
	
	bar := strings.Join(barParts, "")
	
	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground))
	
	// Always show visualizer since we reserved space for it
	visualizer := m.renderVisualizer(visualizerWidth)
	
	return bar + " " + timeStyle.Render(timeStr) + " " + visualizer
}

func formatDurationFromSeconds(seconds float64) string {
	minutes := int(seconds) / 60
	secs := seconds - float64(minutes*60)
	return fmt.Sprintf("%d:%04.1f", minutes, secs)
}

func (m model) renderLibrary() string {
	// Calculate pane widths
	totalWidth := m.width
	if totalWidth < 60 {
		totalWidth = 60
	}
	leftWidth := 25  // Fixed width for categories
	rightWidth := totalWidth - leftWidth - 5 // Account for border and spacing

	// Get raw content for both panes
	theme := m.settingsManager.GetTheme()
	leftLines := m.getLeftPaneLines(theme)
	rightLines := m.getRightPaneLines(theme)
	
	// Ensure both have same number of lines
	maxLines := max(len(leftLines), len(rightLines))
	
	// Pad shorter pane with empty lines
	for len(leftLines) < maxLines {
		leftLines = append(leftLines, "")
	}
	for len(rightLines) < maxLines {
		rightLines = append(rightLines, "")
	}
	
	// Build the display line by line
	var resultLines []string
	for i := 0; i < maxLines; i++ {
		// Left pane content (fixed width)
		leftContent := leftLines[i]
		vWidth := visualWidth(leftContent)
		if vWidth > leftWidth {
			// Truncate while preserving color codes
			leftContent = truncateToWidth(leftContent, leftWidth-3) + "..."
		}
		leftFormatted := padToWidth(leftContent, leftWidth)
		
		// Right pane content
		rightContent := rightLines[i]
		rightVWidth := visualWidth(rightContent)
		if rightVWidth > rightWidth {
			rightContent = truncateToWidth(rightContent, rightWidth-3) + "..."
		}
		
		// Combine with border using theme color
		separator := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Border)).
			Render(" │ ")
		line := leftFormatted + separator + rightContent
		resultLines = append(resultLines, line)
	}
	
	return strings.Join(resultLines, "\n")
}

func (m model) activateControl() (model, tea.Cmd) {
	switch m.controlSelected {
	case 0: // Previous
		if m.playPreviousTrack() {
			return m, tickCmd()
		}
		return m, nil
	case 1: // Play/Pause
		wasPaused := m.audioPlayer.IsPaused()
		m.audioPlayer.TogglePause()
		
		// Handle radio pause tracking
		if m.playingStation != nil {
			if wasPaused {
				// Resuming from pause - update pause tracking
				if m.radioWasPaused {
					m.radioPausedTime += time.Since(m.radioStartTime)
				}
				m.radioStartTime = time.Now()
			} else {
				// Pausing - mark as paused
				m.radioWasPaused = true
			}
		}
		
		if m.audioPlayer.IsPlaying() {
			return m, tickCmd()
		}
		return m, nil
	case 2: // Next
		if m.playNextTrack() {
			return m, tickCmd()
		}
		return m, nil
	case 3: // Stop
		m.audioPlayer.Stop()
		m.playing = ""
		m.playingSong = nil
		m.nowPlayingFocused = false
		return m, nil
	}
	return m, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Calculate visual width of string excluding ANSI color codes
func visualWidth(s string) int {
	// Simple regex to remove ANSI escape sequences
	width := 0
	inEscape := false
	
	for _, r := range s {
		if r == '\033' { // Start of ANSI escape sequence
			inEscape = true
		} else if inEscape && r == 'm' { // End of ANSI escape sequence
			inEscape = false
		} else if !inEscape {
			width++
		}
	}
	
	return width
}

// Pad string to specific visual width (accounting for ANSI codes)
func padToWidth(s string, width int) string {
	vWidth := visualWidth(s)
	if vWidth >= width {
		return s
	}
	padding := strings.Repeat(" ", width-vWidth)
	return s + padding
}

// Truncate string to specific visual width while preserving ANSI codes
func truncateToWidth(s string, maxWidth int) string {
	width := 0
	inEscape := false
	var result strings.Builder
	
	for _, r := range s {
		if r == '\033' { // Start of ANSI escape sequence
			inEscape = true
			result.WriteRune(r)
		} else if inEscape {
			result.WriteRune(r)
			if r == 'm' { // End of ANSI escape sequence
				inEscape = false
			}
		} else {
			if width >= maxWidth {
				break
			}
			result.WriteRune(r)
			width++
		}
	}
	
	return result.String()
}

func (m model) getLeftPaneLines(theme Theme) []string {
	categories := m.libraryBrowser.GetCategories()
	selectedIndex := m.libraryBrowser.GetCategoryIndex()
	isActive := m.libraryBrowser.GetCurrentPane() == "categories"
	
	// Build all lines first
	var allLines []string
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(1)
	allLines = append(allLines, headerStyle.Render("Categories:"))
	allLines = append(allLines, "")
	
	for i, category := range categories {
		selected := i == selectedIndex && isActive
		style := createGradientStyle(selected, 23, theme) // Fixed width for left pane
		
		var prefix string
		if selected {
			prefix = "> "
		} else {
			prefix = "  "
		}
		
		line := style.Render(prefix + category)
		allLines = append(allLines, line)
	}
	
	// Apply viewport - for categories, we don't need complex viewport since there are only 3 categories
	// Just return all lines since the category list is short
	return allLines
}

func (m model) getRightPaneLines(theme Theme) []string {
	contents := m.libraryBrowser.GetContents()
	selectedIndex := m.libraryBrowser.GetContentIndex()
	isActive := m.libraryBrowser.GetCurrentPane() == "contents"
	
	// Build all lines first
	var allLines []string
	var contentStartIndex int = 0
	
	// Add breadcrumb if present
	breadcrumb := m.libraryBrowser.GetBreadcrumb()
	if len(breadcrumb) > 0 {
		theme := m.settingsManager.GetTheme()
		breadcrumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground))
		allLines = append(allLines, breadcrumbStyle.Render("📁 "+strings.Join(breadcrumb, " > ")))
		allLines = append(allLines, "")
		contentStartIndex = 2
	}
	
	// Track which line corresponds to each content item for viewport calculation
	var itemToLineMap []int // Maps content index to line index
	
	for i, item := range contents {
		var prefix string
		var icon string
		
		switch item.Type {
		case "artist":
			icon = "👤 "
		case "album":
			icon = "💿 "
		case "genre":
			icon = "🎭 "
		case "song":
			icon = "♪ "
		default:
			icon = "  "
		}
		
		selected := i == selectedIndex && isActive
		
		if selected {
			prefix = "> "
		} else {
			prefix = "  "
		}
		
		// Track where this item starts in the line array
		itemToLineMap = append(itemToLineMap, len(allLines))
		
		// Calculate available width for right pane
		rightPaneWidth := m.width - 25 - 5 // Total width - left pane - borders
		if rightPaneWidth < 30 {
			rightPaneWidth = 30
		}
		
		// Main title line with gradient
		mainStyle := createGradientStyle(selected, rightPaneWidth, theme)
		mainLine := mainStyle.Render(prefix + icon + item.Title)
		allLines = append(allLines, mainLine)
		
		// Subtitle line if present
		if item.Subtitle != "" {
			subtitleStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Muted)).
				PaddingLeft(5)
			subtitleLine := subtitleStyle.Render(item.Subtitle)
			allLines = append(allLines, subtitleLine)
		}
	}
	
	// Apply viewport to show only visible lines
	viewport := m.libraryBrowser.contentViewport
	if len(allLines) <= viewport.height {
		// All lines fit, return everything
		return allLines
	}
	
	// Calculate viewport based on selected content item
	var viewportTop int
	if selectedIndex < len(itemToLineMap) {
		selectedLineIndex := itemToLineMap[selectedIndex]
		// Adjust for breadcrumb offset
		contentLineIndex := selectedLineIndex - contentStartIndex
		
		// Calculate viewport top to keep selected item visible
		if contentLineIndex < viewport.top {
			viewportTop = contentLineIndex + contentStartIndex
		} else if contentLineIndex >= viewport.top + viewport.height - contentStartIndex {
			viewportTop = contentLineIndex - viewport.height + contentStartIndex + 1
		} else {
			viewportTop = viewport.top + contentStartIndex
		}
		
		// Ensure we don't go negative or past the end
		if viewportTop < 0 {
			viewportTop = 0
		}
		if viewportTop + viewport.height > len(allLines) {
			viewportTop = len(allLines) - viewport.height
		}
	} else {
		viewportTop = viewport.top
	}
	
	// Return only the visible portion
	end := viewportTop + viewport.height
	if end > len(allLines) {
		end = len(allLines)
	}
	
	return allLines[viewportTop:end]
}

func (m model) renderLeftPane(width int) string {
	theme := m.settingsManager.GetTheme()
	
	categoryStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Foreground))

	selectedCategoryStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Background(lipgloss.Color(theme.Muted))

	activeStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Secondary))

	categories := m.libraryBrowser.GetCategories()
	selectedIndex := m.libraryBrowser.GetCategoryIndex()
	isActive := m.libraryBrowser.GetCurrentPane() == "categories"
	
	var items []string
	items = append(items, categoryStyle.Render("Categories:"))
	items = append(items, "")
	
	for i, category := range categories {
		var style lipgloss.Style
		var prefix string
		
		if i == selectedIndex && isActive {
			style = selectedCategoryStyle
			prefix = "> "
		} else if i == selectedIndex {
			style = activeStyle
			prefix = "• "
		} else {
			style = categoryStyle
			prefix = "  "
		}
		
		text := prefix + category
		items = append(items, style.Render(text))
	}
	
	// Ensure the left pane has a minimum height and is properly formatted
	result := lipgloss.JoinVertical(lipgloss.Left, items...)
	
	// Add padding to ensure proper width
	leftPaneStyle := lipgloss.NewStyle().
		Width(width).
		AlignHorizontal(lipgloss.Left)
	
	return leftPaneStyle.Render(result)
}

func (m model) renderRightPane(width int) string {
	theme := m.settingsManager.GetTheme()
	
	contentStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Foreground))

	selectedContentStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Background(lipgloss.Color(theme.Muted))

	contents := m.libraryBrowser.GetContents()
	selectedIndex := m.libraryBrowser.GetContentIndex()
	isActive := m.libraryBrowser.GetCurrentPane() == "contents"
	
	var items []string
	
	// Add breadcrumb if present
	breadcrumb := m.libraryBrowser.GetBreadcrumb()
	if len(breadcrumb) > 0 {
		items = append(items, contentStyle.Render("📁 "+strings.Join(breadcrumb, " > ")))
		items = append(items, "")
	}
	
	for i, item := range contents {
		var style lipgloss.Style
		var prefix string
		var icon string
		
		switch item.Type {
		case "artist":
			icon = "👤 "
		case "album":
			icon = "💿 "
		case "genre":
			icon = "🎭 "
		case "song":
			icon = "♪ "
		default:
			icon = "  "
		}
		
		if i == selectedIndex && isActive {
			style = selectedContentStyle
			prefix = "> "
		} else {
			style = contentStyle
			prefix = "  "
		}
		
		// Main title line
		mainText := prefix + icon + item.Title
		items = append(items, style.Render(mainText))
		
		// Subtitle line if present
		if item.Subtitle != "" {
			subtitleText := "    " + item.Subtitle
			items = append(items, contentStyle.Render(subtitleText))
		}
	}
	
	// Ensure proper width
	result := lipgloss.JoinVertical(lipgloss.Left, items...)
	rightPaneStyle := lipgloss.NewStyle().
		Width(width).
		AlignHorizontal(lipgloss.Left)
	
	return rightPaneStyle.Render(result)
}

func (m model) renderFolderBrowser() string {
	theme := m.settingsManager.GetTheme()
	headerStyle := lipgloss.NewStyle().
		PaddingLeft(2).
		Foreground(lipgloss.Color(theme.Foreground))

	items := []string{
		headerStyle.Render(fmt.Sprintf("📁 Current: %s", m.folderBrowser.GetCurrentPath())),
		"",
	}

	entries := m.folderBrowser.GetVisibleEntries()
	selectedIndex := m.folderBrowser.GetVisibleSelectedIndex()
	
	// Calculate available width for folder browser
	folderWidth := m.width - 4 // Account for padding
	if folderWidth < 30 {
		folderWidth = 30
	}

	for i, entry := range entries {
		isSelected := i == selectedIndex
		var prefix string
		var icon string
		
		if entry == ".." || m.folderBrowser.IsDirectory(filepath.Join(m.folderBrowser.GetCurrentPath(), entry)) {
			icon = "📁 "
		} else {
			icon = "📂 "
		}
		
		if isSelected {
			prefix = "> "
		} else {
			prefix = "  "
		}
		
		// Create gradient style for selection
		theme := m.settingsManager.GetTheme()
		style := createGradientStyle(isSelected, folderWidth, theme)
		displayText := prefix + icon + entry
		
		items = append(items, style.Render(displayText))
	}

	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m model) renderSearch() string {
	if !m.searchMode {
		return ""
	}
	
	// Calculate center position
	searchBoxWidth := 60
	searchBoxHeight := 12
	if m.width < searchBoxWidth + 4 {
		searchBoxWidth = m.width - 4
	}
	
	// Get theme for search styles
	theme := m.settingsManager.GetTheme()
	
	// Create search box style
	searchBoxStyle := lipgloss.NewStyle().
		Width(searchBoxWidth).
		Height(searchBoxHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1).
		Background(lipgloss.Color(theme.Background))
	
	// Search input line
	queryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		Background(lipgloss.Color(theme.Muted)).
		Padding(0, 1).
		Width(searchBoxWidth - 4)
	
	searchPrompt := "🔍 Search: " + m.searchQuery + "█"
	queryLine := queryStyle.Render(searchPrompt)
	
	// Results
	var resultLines []string
	resultLines = append(resultLines, queryLine)
	resultLines = append(resultLines, "")
	
	if len(m.searchResults) == 0 {
		theme := m.settingsManager.GetTheme()
		mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
		if len(m.searchQuery) == 0 {
			resultLines = append(resultLines, mutedStyle.Render("Type to search for songs..."))
		} else {
			resultLines = append(resultLines, mutedStyle.Render("No results found"))
		}
	} else {
		maxResults := min(8, len(m.searchResults))
		for i := 0; i < maxResults; i++ {
			song := m.searchResults[i]
			isSelected := i == m.searchSelected
			var prefix string
			
			if isSelected {
				prefix = "> "
			} else {
				prefix = "  "
			}
			
			songText := prefix + "♪ " + song.Title
			if song.Artist != "Unknown Artist" {
				songText += " - " + song.Artist
			}
			
			// Truncate if too long
			maxLineWidth := searchBoxWidth - 6
			if len(songText) > maxLineWidth {
				songText = songText[:maxLineWidth-3] + "..."
			}
			
			// Create gradient style for selection
			theme := m.settingsManager.GetTheme()
			style := createGradientStyle(isSelected, searchBoxWidth-4, theme)
			resultLines = append(resultLines, style.Render(songText))
		}
	}
	
	// Fill remaining lines
	for len(resultLines) < searchBoxHeight - 2 {
		resultLines = append(resultLines, "")
	}
	
	// Add help text
	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		Italic(true)
	resultLines = append(resultLines, helpStyle.Render("↑/↓ navigate • enter to play • esc to close"))
	
	content := strings.Join(resultLines, "\n")
	searchBox := searchBoxStyle.Render(content)
	
	// Center the search box
	leftPadding := (m.width - searchBoxWidth) / 2
	topPadding := (m.height - searchBoxHeight) / 2
	
	// Create padding
	var lines []string
	for i := 0; i < topPadding; i++ {
		lines = append(lines, "")
	}
	
	searchBoxLines := strings.Split(searchBox, "\n")
	for _, line := range searchBoxLines {
		paddedLine := strings.Repeat(" ", leftPadding) + line
		lines = append(lines, paddedLine)
	}
	
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Playlist management functions
func (m *model) setPlaylist(songs []Song, startIndex int) {
	m.currentPlaylist = songs
	m.currentTrackIndex = startIndex
}

func (m *model) hasNextTrack() bool {
	return m.currentTrackIndex < len(m.currentPlaylist)-1
}

func (m *model) hasPreviousTrack() bool {
	return m.currentTrackIndex > 0
}

func (m *model) playNextTrack() bool {
	if m.hasNextTrack() {
		m.currentTrackIndex++
		return m.playCurrentTrack()
	}
	return false
}

func (m *model) playPreviousTrack() bool {
	if m.hasPreviousTrack() {
		m.currentTrackIndex--
		return m.playCurrentTrack()
	}
	return false
}

func (m *model) playCurrentTrack() bool {
	if m.currentTrackIndex >= 0 && m.currentTrackIndex < len(m.currentPlaylist) {
		song := m.currentPlaylist[m.currentTrackIndex]
		if err := m.audioPlayer.Play(song.FilePath); err == nil {
			m.playing = song.Title
			m.playingSong = &song
			m.audioPlayer.SetDuration(song.DurationSecs)
			return true
		}
	}
	return false
}

func (m *model) isTrackFinished() bool {
	if m.playingSong == nil {
		return false
	}
	
	// If the audio player is not playing and not paused, the track has finished
	if !m.audioPlayer.IsPlaying() && !m.audioPlayer.IsPaused() {
		return true
	}
	
	// Also check if we're near the end of the track as a backup
	position := m.audioPlayer.GetPosition()
	duration := m.audioPlayer.GetDuration()
	
	// Consider track finished if we're within 1 second of the end
	return duration > 0 && position >= duration-1
}

func (m *model) createPlaylistFromContext(selectedSong *Song) []Song {
	// Get current context from library browser
	breadcrumb := m.libraryBrowser.GetBreadcrumb()
	
	if len(breadcrumb) == 0 {
		// Top level - return all songs in current category
		return m.getCurrentCategorySongs()
	} else if len(breadcrumb) == 1 {
		// Artist or Genre level - return songs from current artist/genre
		return m.getSongsFromCurrentContext()
	} else {
		// Album level - return songs from current album
		return m.getSongsFromCurrentContext()
	}
}

func (m *model) getCurrentCategorySongs() []Song {
	allSongs := m.libraryManager.GetSongs()
	categoryType := m.libraryBrowser.GetCategoryType()
	
	// Return all songs, but sorted by the current category for consistent order
	switch categoryType {
	case "artists":
		sort.Slice(allSongs, func(i, j int) bool {
			artistI := allSongs[i].Artist
			if artistI == "" {
				artistI = "Unknown Artist"
			}
			artistJ := allSongs[j].Artist
			if artistJ == "" {
				artistJ = "Unknown Artist"
			}
			if artistI == artistJ {
				return strings.ToLower(allSongs[i].Title) < strings.ToLower(allSongs[j].Title)
			}
			return strings.ToLower(artistI) < strings.ToLower(artistJ)
		})
	case "albums":
		sort.Slice(allSongs, func(i, j int) bool {
			albumI := allSongs[i].Album
			if albumI == "" {
				albumI = "Unknown Album"
			}
			albumJ := allSongs[j].Album
			if albumJ == "" {
				albumJ = "Unknown Album"
			}
			if albumI == albumJ {
				return strings.ToLower(allSongs[i].Title) < strings.ToLower(allSongs[j].Title)
			}
			return strings.ToLower(albumI) < strings.ToLower(albumJ)
		})
	case "genres":
		sort.Slice(allSongs, func(i, j int) bool {
			genreI := allSongs[i].Genre
			if genreI == "" {
				genreI = "Unknown Genre"
			}
			genreJ := allSongs[j].Genre
			if genreJ == "" {
				genreJ = "Unknown Genre"
			}
			if genreI == genreJ {
				return strings.ToLower(allSongs[i].Title) < strings.ToLower(allSongs[j].Title)
			}
			return strings.ToLower(genreI) < strings.ToLower(genreJ)
		})
	}
	
	return allSongs
}

func (m *model) getSongsFromCurrentContext() []Song {
	breadcrumb := m.libraryBrowser.GetBreadcrumb()
	allSongs := m.libraryManager.GetSongs()
	
	if len(breadcrumb) == 0 {
		// Top level - return all songs in current category
		return m.getCurrentCategorySongs()
	} else if len(breadcrumb) == 1 {
		// Artist or Genre level
		categoryType := m.libraryBrowser.GetCategoryType()
		context := breadcrumb[0]
		
		var filteredSongs []Song
		for _, song := range allSongs {
			switch categoryType {
			case "artists":
				artist := song.Artist
				if artist == "" {
					artist = "Unknown Artist"
				}
				if artist == context {
					filteredSongs = append(filteredSongs, song)
				}
			case "genres":
				genre := song.Genre
				if genre == "" {
					genre = "Unknown Genre"
				}
				if genre == context {
					filteredSongs = append(filteredSongs, song)
				}
			}
		}
		return filteredSongs
	} else if len(breadcrumb) == 2 {
		// Album level
		categoryType := m.libraryBrowser.GetCategoryType()
		context1 := breadcrumb[0] // Artist or Genre
		context2 := breadcrumb[1] // Album
		
		var filteredSongs []Song
		for _, song := range allSongs {
			switch categoryType {
			case "artists":
				artist := song.Artist
				if artist == "" {
					artist = "Unknown Artist"
				}
				album := song.Album
				if album == "" {
					album = "Unknown Album"
				}
				if artist == context1 && album == context2 {
					filteredSongs = append(filteredSongs, song)
				}
			}
		}
		
		// Sort album songs by track number if available, otherwise by title
		sort.Slice(filteredSongs, func(i, j int) bool {
			// If both songs have track numbers, sort by track number
			if filteredSongs[i].TrackNumber > 0 && filteredSongs[j].TrackNumber > 0 {
				return filteredSongs[i].TrackNumber < filteredSongs[j].TrackNumber
			}
			// If only one has a track number, prioritize it
			if filteredSongs[i].TrackNumber > 0 && filteredSongs[j].TrackNumber == 0 {
				return true
			}
			if filteredSongs[i].TrackNumber == 0 && filteredSongs[j].TrackNumber > 0 {
				return false
			}
			// If neither has track numbers, sort alphabetically by title
			return strings.ToLower(filteredSongs[i].Title) < strings.ToLower(filteredSongs[j].Title)
		})
		
		return filteredSongs
	}
	
	// Default fallback
	return allSongs
}

func (m *model) findSongInPlaylist(playlist []Song, targetSong *Song) int {
	for i, song := range playlist {
		if song.FilePath == targetSong.FilePath {
			return i
		}
	}
	return -1
}

// Fuzzy search functionality
type searchMatch struct {
	song  Song
	score int
}

func (m *model) performSearch(query string) {
	if len(query) == 0 {
		m.searchResults = []Song{}
		return
	}
	
	allSongs := m.libraryManager.GetSongs()
	var matches []searchMatch
	
	queryLower := strings.ToLower(query)
	
	for _, song := range allSongs {
		score := calculateMatchScore(song, queryLower)
		if score > 0 {
			matches = append(matches, searchMatch{song: song, score: score})
		}
	}
	
	// Sort by score (higher is better)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})
	
	// Extract top 20 results
	m.searchResults = []Song{}
	maxResults := 20
	for i, match := range matches {
		if i >= maxResults {
			break
		}
		m.searchResults = append(m.searchResults, match.song)
	}
	
	m.searchSelected = 0
}

func calculateMatchScore(song Song, query string) int {
	title := strings.ToLower(song.Title)
	artist := strings.ToLower(song.Artist)
	album := strings.ToLower(song.Album)
	
	score := 0
	
	// Exact matches get highest score
	if strings.Contains(title, query) {
		score += 100
	}
	if strings.Contains(artist, query) {
		score += 80
	}
	if strings.Contains(album, query) {
		score += 60
	}
	
	// Fuzzy matching - check if all characters of query appear in order
	if fuzzyMatch(title, query) {
		score += 50
	}
	if fuzzyMatch(artist, query) {
		score += 40
	}
	if fuzzyMatch(album, query) {
		score += 30
	}
	
	// Prefix matching gets bonus points
	if strings.HasPrefix(title, query) {
		score += 200
	}
	if strings.HasPrefix(artist, query) {
		score += 150
	}
	
	return score
}

func fuzzyMatch(text, pattern string) bool {
	textLen := len(text)
	patternLen := len(pattern)
	
	if patternLen == 0 {
		return true
	}
	if textLen == 0 {
		return false
	}
	
	i, j := 0, 0
	for i < textLen && j < patternLen {
		if text[i] == pattern[j] {
			j++
		}
		i++
	}
	
	return j == patternLen
}

func (m model) renderRadio() string {
	currentView := m.radioBrowser.GetCurrentView()
	
	if currentView == "add" {
		return m.renderRadioAddForm()
	} else if currentView == "quickadd" {
		return m.renderRadioQuickAdd()
	} else {
		return m.renderRadioList()
	}
}

func (m model) renderRadioList() string {
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(1)
	
	stationStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Foreground))
	
	selectedStationStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Background(lipgloss.Color(theme.Muted))
	
	stations := m.radioBrowser.GetStations()
	selected := m.radioBrowser.GetSelected()
	
	var items []string
	items = append(items, headerStyle.Render("📻 Radio Stations"))
	items = append(items, "")
	
	if len(stations) == 0 {
		items = append(items, stationStyle.Render("No radio stations added yet."))
		items = append(items, "")
		items = append(items, stationStyle.Render("Press 'a' to add a station."))
	} else {
		for i, station := range stations {
			var style lipgloss.Style
			var prefix string
			
			if i == selected {
				style = selectedStationStyle
				prefix = "> "
			} else {
				style = stationStyle
				prefix = "  "
			}
			
			name := fmt.Sprintf("%s%s", prefix, station.Name)
			subtitle := fmt.Sprintf("    %s", station.URL)
			if station.Genre != "" {
				subtitle += fmt.Sprintf(" • %s", station.Genre)
			}
			
			items = append(items, style.Render(name))
			items = append(items, stationStyle.Render(subtitle))
			items = append(items, "")
		}
		
		items = append(items, "")
		items = append(items, stationStyle.Render("Press 'a' to add a station, 'enter' to play selected station."))
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m model) renderRadioAddForm() string {
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(1)
	
	labelStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Foreground))
	
	selectedLabelStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	
	inputStyle := lipgloss.NewStyle().
		PaddingLeft(3).
		Foreground(lipgloss.Color(theme.Foreground)).
		Background(lipgloss.Color(theme.Muted))
	
	selectedInputStyle := lipgloss.NewStyle().
		PaddingLeft(3).
		Foreground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.Muted)).
		Bold(true)
	
	formField := m.radioBrowser.GetFormField()
	name, url, genre, language, country, description, tags := m.radioBrowser.GetFormData()
	
	// If in input mode, show input buffer for current field
	if m.radioBrowser.IsInputMode() {
		switch formField {
		case 0:
			name = m.radioBrowser.GetInputBuffer()
		case 1:
			url = m.radioBrowser.GetInputBuffer()
		case 2:
			genre = m.radioBrowser.GetInputBuffer()
		case 3:
			language = m.radioBrowser.GetInputBuffer()
		case 4:
			country = m.radioBrowser.GetInputBuffer()
		case 5:
			description = m.radioBrowser.GetInputBuffer()
		case 6:
			tags = m.radioBrowser.GetInputBuffer()
		}
	}
	
	var items []string
	items = append(items, headerStyle.Render("📻 Add Radio Station"))
	items = append(items, "")
	
	fields := []struct {
		label string
		value string
		index int
	}{
		{"Name:", name, 0},
		{"URL:", url, 1},
		{"Genre:", genre, 2},
		{"Language:", language, 3},
		{"Country:", country, 4},
		{"Description:", description, 5},
		{"Tags (comma-separated):", tags, 6},
	}
	
	for _, field := range fields {
		var labelStyleToUse lipgloss.Style
		var inputStyleToUse lipgloss.Style
		
		if field.index == formField {
			labelStyleToUse = selectedLabelStyle
			inputStyleToUse = selectedInputStyle
		} else {
			labelStyleToUse = labelStyle
			inputStyleToUse = inputStyle
		}
		
		items = append(items, labelStyleToUse.Render(field.label))
		inputText := field.value
		if inputText == "" {
			inputText = "(empty)"
		}
		items = append(items, inputStyleToUse.Render(inputText))
		items = append(items, "")
	}
	
	items = append(items, "")
	if m.radioBrowser.IsInputMode() {
		items = append(items, labelStyle.Render("Enter to save, Escape to cancel input"))
	} else {
		items = append(items, labelStyle.Render("↑/↓ to navigate, Enter to edit field, 's' to save, Escape to cancel"))
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m model) renderRadioQuickAdd() string {
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(1)
	
	labelStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Foreground))
	
	selectedLabelStyle := lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	
	inputStyle := lipgloss.NewStyle().
		PaddingLeft(3).
		Foreground(lipgloss.Color(theme.Foreground)).
		Background(lipgloss.Color(theme.Muted))
	
	selectedInputStyle := lipgloss.NewStyle().
		PaddingLeft(3).
		Foreground(lipgloss.Color(theme.Primary)).
		Background(lipgloss.Color(theme.Muted)).
		Bold(true)
	
	formField := m.radioBrowser.GetFormField()
	quickURL := m.radioBrowser.GetQuickURL()
	quickName := m.radioBrowser.GetQuickName()
	
	// If in input mode, show input buffer for current field
	if m.radioBrowser.IsInputMode() {
		switch formField {
		case 0:
			quickURL = m.radioBrowser.GetInputBuffer()
		case 1:
			quickName = m.radioBrowser.GetInputBuffer()
		}
	}
	
	var items []string
	items = append(items, headerStyle.Render("🎆 Quick Start - Start Listening Now!"))
	items = append(items, "")
	items = append(items, labelStyle.Render("Paste a radio station URL below to start listening immediately:"))
	items = append(items, "")
	
	fields := []struct {
		label string
		value string
		index int
	}{
		{"Station URL:", quickURL, 0},
		{"Station Name (optional):", quickName, 1},
	}
	
	for _, field := range fields {
		var labelStyleToUse lipgloss.Style
		var inputStyleToUse lipgloss.Style
		
		if field.index == formField {
			labelStyleToUse = selectedLabelStyle
			inputStyleToUse = selectedInputStyle
		} else {
			labelStyleToUse = labelStyle
			inputStyleToUse = inputStyle
		}
		
		items = append(items, labelStyleToUse.Render(field.label))
		inputText := field.value
		if inputText == "" {
			inputText = "(empty)"
		}
		items = append(items, inputStyleToUse.Render(inputText))
		items = append(items, "")
	}
	
	items = append(items, "")
	if m.radioBrowser.IsInputMode() {
		items = append(items, labelStyle.Render("Enter to save, Escape to cancel input"))
	} else {
		items = append(items, labelStyle.Render("↑/↓ navigate, Enter to edit, 'p' to play now, 's' to save & play, 'a' for full form, Escape to cancel"))
	}
	
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m model) handleRadioEnter() (model, tea.Cmd) {
	if m.radioBrowser.GetCurrentView() == "add" {
		if m.radioBrowser.IsInputMode() {
			m.radioBrowser.FinishInput()
		} else {
			m.radioBrowser.StartInput()
		}
	} else if m.radioBrowser.GetCurrentView() == "quickadd" {
		if m.radioBrowser.IsInputMode() {
			m.radioBrowser.FinishInput()
		} else {
			m.radioBrowser.StartInput()
		}
	} else {
		// Playing a radio station
		if station := m.radioBrowser.EnterSelected(); station != nil {
			playURL := station.StreamURL
			if playURL == "" {
				playURL = station.URL
			}
			if err := m.audioPlayer.Play(playURL); err == nil {
				m.playing = station.Name
				m.playingSong = nil
				m.playingStation = station
				m.radioStartTime = time.Now()
				m.radioPausedTime = 0
				m.radioWasPaused = false
				return m, tickCmd()
			}
		}
	}
	return m, nil
}

func (m model) renderSettings() string {
	switch m.settingsBrowser.GetCurrentView() {
	case "main":
		return m.renderSettingsMain()
	case "themes":
		return m.renderSettingsThemes()
	case "confirm_clear_music":
		return m.renderSettingsConfirmClearMusic()
	case "confirm_clear_radio":
		return m.renderSettingsConfirmClearRadio()
	default:
		return "Unknown settings view"
	}
}

func (m model) renderSettingsMain() string {
	var items []string
	
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Padding(1, 0)
	
	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		PaddingLeft(2)
	
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(theme.Primary)).
		PaddingLeft(2)
	
	items = append(items, headerStyle.Render("Settings"))
	items = append(items, "")
	
	menuItems := []string{
		"Clear Music Library",
		"Clear Radio Library", 
		"Color Themes",
	}
	
	for i, item := range menuItems {
		style := normalStyle
		if i == m.settingsBrowser.GetSelected() {
			style = selectedStyle
		}
		items = append(items, style.Render(item))
	}
	
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("Use arrow keys to navigate, Enter to select, Escape to go back"))
	
	return strings.Join(items, "\n")
}

func (m model) renderSettingsThemes() string {
	var items []string
	
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Padding(1, 0)
	
	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		PaddingLeft(2)
	
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(theme.Primary)).
		PaddingLeft(2)
	
	currentTheme := m.settingsManager.GetSettings().Theme
	
	items = append(items, headerStyle.Render("Color Themes"))
	items = append(items, "")
	
	themeNames := m.settingsBrowser.GetThemeNames()
	
	for i, themeName := range themeNames {
		displayName := themeName
		if themeName == currentTheme {
			displayName += " (current)"
		}
		
		style := normalStyle
		if i == m.settingsBrowser.GetThemeSelected() {
			style = selectedStyle
		}
		items = append(items, style.Render(displayName))
	}
	
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("Use arrow keys to navigate, Enter to select theme, Escape to go back"))
	
	return strings.Join(items, "\n")
}

func (m model) renderSettingsConfirmClearMusic() string {
	var items []string
	
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Error)).
		Bold(true).
		Padding(1, 0)
	
	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		PaddingLeft(2)
	
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(theme.Error)).
		PaddingLeft(2)
	
	items = append(items, headerStyle.Render("⚠️  Clear Music Library"))
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground)).Render("Are you sure you want to clear your entire music library?"))
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("This will remove all songs and folder references."))
	items = append(items, "")
	
	buttons := []string{"Cancel", "Confirm"}
	
	for i, button := range buttons {
		style := normalStyle
		if i == m.settingsBrowser.GetConfirmSelected() {
			style = selectedStyle
		}
		items = append(items, style.Render(button))
	}
	
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("Use arrow keys to navigate, Enter to confirm, Escape to cancel"))
	
	return strings.Join(items, "\n")
}

func (m model) renderSettingsConfirmClearRadio() string {
	var items []string
	
	theme := m.settingsManager.GetTheme()
	
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Error)).
		Bold(true).
		Padding(1, 0)
	
	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		PaddingLeft(2)
	
	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(theme.Error)).
		PaddingLeft(2)
	
	items = append(items, headerStyle.Render("⚠️  Clear Radio Library"))
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground)).Render("Are you sure you want to clear your entire radio library?"))
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("This will remove all saved radio stations."))
	items = append(items, "")
	
	buttons := []string{"Cancel", "Confirm"}
	
	for i, button := range buttons {
		style := normalStyle
		if i == m.settingsBrowser.GetConfirmSelected() {
			style = selectedStyle
		}
		items = append(items, style.Render(button))
	}
	
	items = append(items, "")
	items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Render("Use arrow keys to navigate, Enter to confirm, Escape to cancel"))
	
	return strings.Join(items, "\n")
}

// createVisualizer creates a new music visualizer (placeholder function)
func createVisualizer(theme Theme) barchart.Model {
	// We're now using a custom horizontal visualizer instead of ntcharts
	// This function is kept for compatibility but returns empty chart
	return barchart.New(1, 1)
}

// generateMockAudioData generates mock frequency data for the visualizer
func generateMockAudioData(isPlaying bool) []barchart.BarData {
	data := make([]barchart.BarData, 12)
	
	if !isPlaying {
		// Return silent data when not playing
		for i := 0; i < len(data); i++ {
			data[i] = barchart.BarData{
				Label: fmt.Sprintf("F%d", i+1),
				Values: []barchart.BarValue{
					{"", 0.0, lipgloss.NewStyle().Foreground(lipgloss.Color("240"))},
				},
			}
		}
		return data
	}
	
	// Generate mock frequency data with realistic patterns
	for i := 0; i < len(data); i++ {
		// Lower frequencies (bass) tend to have more energy
		baseValue := 0.3
		if i < 3 {
			baseValue = 0.6 // More bass
		} else if i < 6 {
			baseValue = 0.4 // Mid frequencies
		} else {
			baseValue = 0.2 // Higher frequencies
		}
		
		// Add some randomness to simulate real audio
		randomFactor := 0.3 + rand.Float64()*0.7
		
		// Add some periodic variation to make it look more musical
		timeVariation := 0.8 + 0.2*math.Sin(float64(time.Now().UnixNano()/1000000)*0.001*float64(i+1))
		
		value := baseValue * randomFactor * timeVariation
		if value > 1.0 {
			value = 1.0
		}
		
		// Use different colors for different frequency ranges
		var barColor string
		if i < 3 {
			barColor = "9"  // Red for bass
		} else if i < 6 {
			barColor = "11" // Yellow for mid
		} else {
			barColor = "10" // Green for highs
		}
		
		data[i] = barchart.BarData{
			Label: fmt.Sprintf("F%d", i+1),
			Values: []barchart.BarValue{
				{"", value, lipgloss.NewStyle().Foreground(lipgloss.Color(barColor))},
			},
		}
	}
	
	return data
}

// renderVisualizer renders the music visualizer using real audio data
func (m *model) renderVisualizer(width int) string {
	isPlaying := m.audioPlayer.IsPlaying()
	
	if !isPlaying {
		// Show flat line when not playing
		return strings.Repeat("▁", width)
	}
	
	// Get real audio samples from the audio player
	audioSamples := m.audioPlayer.GetAudioSamples()
	
	// Create bars based on available width
	numBars := width
	var bars []string
	
	for i := 0; i < numBars; i++ {
		var amplitude float64
		
		// If we have audio samples, use them
		if len(audioSamples) > 0 {
			// Map bar index to sample index
			sampleIndex := (i * len(audioSamples)) / numBars
			if sampleIndex >= len(audioSamples) {
				sampleIndex = len(audioSamples) - 1
			}
			amplitude = audioSamples[sampleIndex]
			
			// Amplify the signal for better visualization
			amplitude *= 3.0
			if amplitude > 1.0 {
				amplitude = 1.0
			}
		} else {
			// Fallback to flat line if no audio data
			amplitude = 0.0
		}
		
		// Convert amplitude to bar height (0-8 levels)
		height := int(amplitude * 8)
		
		// Choose bar character based on height
		var barChar string
		switch height {
		case 0:
			barChar = "▁"
		case 1:
			barChar = "▂"
		case 2:
			barChar = "▃"
		case 3:
			barChar = "▄"
		case 4:
			barChar = "▅"
		case 5:
			barChar = "▆"
		case 6:
			barChar = "▇"
		default:
			barChar = "█"
		}
		
		// Color the bar based on frequency range (simulated)
		var style lipgloss.Style
		if i < numBars/4 {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // Red for bass
		} else if i < numBars/2 {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // Yellow for mid
		} else {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // Green for highs
		}
		
		bars = append(bars, style.Render(barChar))
	}
	
	return strings.Join(bars, "")
}

func main() {
	m := initialModel()
	defer m.audioPlayer.Close()
	
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}