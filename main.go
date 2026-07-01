package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/NimbleMarkets/ntcharts/v2/barchart"
	"github.com/NimbleMarkets/ntcharts/v2/linechart/wavelinechart"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"github.com/NimbleMarkets/ntcharts/v2/canvas/runes"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second/20, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// scanState holds the live progress of a library scan running on a background
// goroutine. The scan goroutine writes via atomics; the UI goroutine reads them
// on each scanTickMsg, so there is no shared-memory data race.
type scanState struct {
	done  atomic.Int64
	total atomic.Int64
}

// scanTickMsg drives the progress-bar refresh while a scan is in flight.
type scanTickMsg struct{}

// scanDoneMsg is returned by the scan command when the background walk finishes.
type scanDoneMsg struct {
	mode   string // "add" or "rescan"
	folder string // folder added, when mode == "add"
	songs  []Song
}

func scanTickCmd() tea.Cmd {
	return tea.Tick(time.Second/15, func(t time.Time) tea.Msg {
		return scanTickMsg{}
	})
}

// startScanCmd walks the given folders on a background goroutine, reporting
// progress through st, and returns a scanDoneMsg when complete.
func startScanCmd(folders []string, mode, folder string, st *scanState) tea.Cmd {
	return func() tea.Msg {
		songs := scanFoldersProgress(folders, func(done, total int) {
			st.total.Store(int64(total))
			st.done.Store(int64(done))
		})
		return scanDoneMsg{mode: mode, folder: folder, songs: songs}
	}
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
	searchCategory    string // "songs", "artists", "albums", "genres"
	searchResults     []searchResult
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
	currentChartType  string // "unicode", "bars", "line", "wave", "sparkline", "heatmap"
	// Library scan progress
	scanning     bool
	scanState    *scanState
	scanPercent  float64
	scanDone     int
	scanTotal    int
	scanLabel    string
	scanProgress progress.Model
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
		currentChartType:  "unicode", // Start with the high-res unicode visualizer
		scanProgress: progress.New(progress.WithColors(
			lipgloss.Color(settingsManager.GetTheme().Primary),
			lipgloss.Color(settingsManager.GetTheme().Secondary),
		)),
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

	case scanTickMsg:
		if m.scanning && m.scanState != nil {
			m.scanDone = int(m.scanState.done.Load())
			m.scanTotal = int(m.scanState.total.Load())
			if m.scanTotal > 0 {
				m.scanPercent = float64(m.scanDone) / float64(m.scanTotal)
			}
			return m, scanTickCmd()
		}
		return m, nil

	case scanDoneMsg:
		m.scanning = false
		m.scanState = nil
		m.scanPercent = 0
		if msg.songs != nil || msg.mode == "rescan" {
			switch msg.mode {
			case "add":
				m.libraryManager.AddFolderWithSongs(msg.folder, msg.songs)
			case "rescan":
				m.libraryManager.SetSongs(msg.songs)
			}
			m.libraryBrowser.Refresh()
			m.currentView = "library"
			m.selected = 0
			m.libraryBrowser.ForceViewportReset()
		}
		return m, nil

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

	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)

	case tea.MouseMotionMsg:
		// Left-drag across the progress bar scrubs/seeks.
		if msg.Button == tea.MouseLeft {
			m.seekFromMouse(msg)
		}
		return m, nil

	case tea.KeyPressMsg:
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
				m.closeSearch()
				return m, nil
			case "tab":
				m.cycleSearchCategory(1)
				return m, nil
			case "shift+tab":
				m.cycleSearchCategory(-1)
				return m, nil
			case "enter":
				if m.playSelectedSearchResult() {
					m.closeSearch()
					return m, tickCmd()
				}
				return m, nil
			case "ctrl+a":
				// Play everything currently listed (e.g. all jazz sub-genres).
				if m.playAllSearchResults() {
					m.closeSearch()
					return m, tickCmd()
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
			case "space", " ":
				// Space arrives as the named key "space", not a printable rune.
				m.searchQuery += " "
				m.performSearch(m.searchQuery)
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
		case "/", "ctrl+k":
			// Enter search mode (Spotlight-style, works from any tab)
			m.searchMode = true
			m.searchQuery = ""
			m.searchCategory = "songs"
			m.searchResults = nil
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
				m.currentView = "visualizer"
				m.selected = 0
			} else if m.currentView == "visualizer" {
				m.currentView = "settings"
				m.selected = 0
			} else if m.currentView == "settings" {
				m.currentView = "library"
				m.selected = 0
				m.libraryBrowser.ResetViewport()
			}
			
			// Reset viewport whenever switching TO library view
			if m.currentView == "library" {
				m.libraryBrowser.ForceViewportReset()
				// Also ensure the viewport height is properly set
				headerHeight := 1
				tabsHeight := 1
				statusHeight := 1
				nowPlayingHeight := 0
				if m.playingSong != nil || m.playingStation != nil {
					nowPlayingHeight = m.calculateNowPlayingHeight()
				}
				contentHeight := m.height - headerHeight - tabsHeight - statusHeight - nowPlayingHeight - 3
				if contentHeight < 5 {
					contentHeight = 5
				}
				m.libraryBrowser.SetViewportHeight(contentHeight)
			}
			return m, nil
		case "a":
			if m.currentView == "folder" {
				if selected := m.folderBrowser.GetSelected(); selected != "" {
					if m.folderBrowser.IsDirectory(selected) && !m.scanning {
						// Scan the folder on a background goroutine so the UI
						// stays responsive and can show a progress bar.
						m.scanning = true
						m.scanState = &scanState{}
						m.scanPercent = 0
						m.scanDone, m.scanTotal = 0, 0
						m.scanLabel = "Adding folder: " + selected
						return m, tea.Batch(startScanCmd([]string{selected}, "add", selected, m.scanState), scanTickCmd())
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
			if m.currentView == "library" && !m.scanning {
				// Rescan all library folders on a background goroutine.
				if folders := m.libraryManager.GetFolders(); len(folders) > 0 {
					m.scanning = true
					m.scanState = &scanState{}
					m.scanPercent = 0
					m.scanDone, m.scanTotal = 0, 0
					m.scanLabel = "Rescanning library…"
					return m, tea.Batch(startScanCmd(folders, "rescan", "", m.scanState), scanTickCmd())
				}
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
			} else if m.currentView == "visualizer" {
				// No up/down navigation needed for visualizer
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
			} else if m.currentView == "visualizer" {
				// No up/down navigation needed for visualizer
			} else if m.currentView == "settings" {
				m.settingsBrowser.MoveDown()
			}
			return m, nil
		case "left", "h":
			if m.nowPlayingFocused {
				if m.controlSelected > 0 {
					m.controlSelected--
				}
			} else if m.currentView == "visualizer" {
				// Switch to previous chart type
				chartTypes := []string{"unicode", "bars", "line", "wave"}
				currentIndex := 0
				for i, chartType := range chartTypes {
					if chartType == m.currentChartType {
						currentIndex = i
						break
					}
				}
				if currentIndex > 0 {
					m.currentChartType = chartTypes[currentIndex-1]
				} else {
					m.currentChartType = chartTypes[len(chartTypes)-1] // Wrap to last
				}
			}
			return m, nil
		case "right", "l":
			if m.nowPlayingFocused {
				if m.controlSelected < 3 {
					m.controlSelected++
				}
			} else if m.currentView == "visualizer" {
				// Switch to next chart type
				chartTypes := []string{"unicode", "bars", "line", "wave"}
				currentIndex := 0
				for i, chartType := range chartTypes {
					if chartType == m.currentChartType {
						currentIndex = i
						break
					}
				}
				if currentIndex < len(chartTypes)-1 {
					m.currentChartType = chartTypes[currentIndex+1]
				} else {
					m.currentChartType = chartTypes[0] // Wrap to first
				}
			}
			return m, nil
		case "enter":
			if m.nowPlayingFocused {
				return m.activateControl()
			} else if m.currentView == "library" {
				return m.enterLibrarySelection()
			} else if m.currentView == "folder" {
				m.enterFolderSelection()
			} else if m.currentView == "radio" {
				return m.handleRadioEnter()
			} else if m.currentView == "visualizer" {
				// No specific enter action needed for visualizer
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
			} else if m.currentView == "visualizer" {
				// No escape action needed for visualizer
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
			} else if m.currentView == "visualizer" {
				// No backspace action needed for visualizer
			}
			return m, nil
		default:
			return m, nil
		}
	}
	return m, nil
}

func (m model) View() tea.View {
	// zone.Scan records the screen positions of all marked clickable regions.
	v := tea.NewView(zone.Scan(m.renderView()))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderScanningView renders a centered progress panel shown while a library
// scan runs on a background goroutine, so a large library no longer looks like
// a frozen app.
func (m model) renderScanningView(height int) string {
	theme := m.settingsManager.GetTheme()

	barWidth := m.width - 20
	if barWidth > 60 {
		barWidth = 60
	}
	if barWidth < 10 {
		barWidth = 10
	}
	prog := m.scanProgress
	prog.SetWidth(barWidth)
	bar := prog.ViewAs(m.scanPercent)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))

	count := fmt.Sprintf("%d / %d tracks", m.scanDone, m.scanTotal)
	if m.scanTotal == 0 {
		count = "Counting tracks…"
	}

	panel := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(m.spinner.View()+" "+m.scanLabel),
		"",
		bar,
		"",
		countStyle.Render(count),
	)

	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (m model) renderView() string {
	// Calculate fixed component heights
	headerHeight := 1
	// Tabs are bordered (3 rows tall), so measure the real height instead of
	// assuming 1 — otherwise the content area is over-estimated and the bottom
	// rows (including the selection) get truncated by the topSection MaxHeight.
	tabs := m.renderTabs()
	tabsHeight := lipgloss.Height(tabs)
	statusHeight := 1
	nowPlayingHeight := 0
	if m.playingSong != nil || m.playingStation != nil {
		nowPlayingHeight = m.calculateNowPlayingHeight()
	}

	// Calculate content viewport height
	// Account for all fixed elements plus spacing
	spacing := 3 // Spacing between sections
	if nowPlayingHeight > 0 {
		spacing = 4 // Extra spacing when now playing is shown
	}
	availableHeight := m.height - headerHeight - tabsHeight - statusHeight - nowPlayingHeight - spacing
	if availableHeight < 5 {
		availableHeight = 5
	}

	// Keep the scrollable browsers sized to exactly this visible content area on
	// every frame — including when the now-playing box appears or disappears —
	// so the selected row can never scroll off-screen. The browsers are
	// pointers, so these updates persist.
	m.libraryBrowser.SetViewportHeight(availableHeight)
	// The folder view renders a 2-line header (current path + blank) above its
	// entries, so give it two fewer rows to keep the selection on-screen.
	m.folderBrowser.SetViewportHeight(availableHeight - 2)

	// Render fixed components
	theme := m.settingsManager.GetTheme()
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		PaddingLeft(2)

	statusStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
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
	// Truncate the header so a long breadcrumb or browse path can't overflow the
	// terminal width (headerStyle adds PaddingLeft(2)).
	maxHeaderWidth := m.width - 2
	if maxHeaderWidth < 1 {
		maxHeaderWidth = 1
	}
	header := headerStyle.Render(ansi.Truncate(headerText, maxHeaderWidth, "…"))

	// Render main content with viewport
	var content string
	if m.scanning {
		content = m.renderScanningView(availableHeight)
	} else {
		content = m.renderMainContent(availableHeight)
	}
	
	// Render status
	playStatus := "⏹  Stopped"
	if m.audioPlayer.IsPlaying() {
		playStatus = "▶  Playing"
	} else if m.audioPlayer.IsPaused() {
		playStatus = "⏸  Paused"
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

	// Build the layout with fixed positioning
	// Top section (header + tabs + content)
	topSection := lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabs,
		"",
		content,
	)
	
	// Bottom section (now playing + status)
	var bottomSection string
	if m.playingSong != nil || m.playingStation != nil {
		bottomSection = lipgloss.JoinVertical(lipgloss.Left,
			m.renderNowPlaying(),
			"",
			status,
		)
	} else {
		bottomSection = status
	}
	
	// Calculate the height for the top section
	bottomSectionHeight := statusHeight
	if m.playingSong != nil || m.playingStation != nil {
		bottomSectionHeight = nowPlayingHeight + statusHeight + 1 // +1 for spacing
	}
	
	// Use Place to position the bottom section at the bottom
	fullHeight := m.height
	topSectionHeight := fullHeight - bottomSectionHeight - 1
	
	// Ensure the top section fills its allocated space
	topSectionStyled := lipgloss.NewStyle().
		Height(topSectionHeight).
		MaxHeight(topSectionHeight).
		Render(topSection)
	
	// Combine top and bottom sections
	mainView := lipgloss.JoinVertical(lipgloss.Left,
		topSectionStyled,
		"",
		bottomSection,
	)
	
	// Hard-clip every line to the terminal width so that any long content
	// (status help text, radio rows, deep paths, …) can't wrap onto a second
	// visual row and break the layout. The per-component truncation above keeps
	// things tidy with ellipses; this just guarantees correctness.
	mainView = lipgloss.NewStyle().MaxWidth(m.width).Render(mainView)

	// Overlay search if active: float the modal over a dimmed copy of the app.
	if m.searchMode {
		return m.renderSearch(mainView)
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
	visualizerTab := "Visualizer"
	settingsTab := "Settings"
	
	if m.currentView == "library" {
		libraryTab = activeTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		visualizerTab = inactiveTabStyle.Render(visualizerTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "folder" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = activeTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		visualizerTab = inactiveTabStyle.Render(visualizerTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "radio" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = activeTabStyle.Render(radioTab)
		visualizerTab = inactiveTabStyle.Render(visualizerTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "visualizer" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		visualizerTab = activeTabStyle.Render(visualizerTab)
		settingsTab = inactiveTabStyle.Render(settingsTab)
	} else if m.currentView == "settings" {
		libraryTab = inactiveTabStyle.Render(libraryTab)
		filesTab = inactiveTabStyle.Render(filesTab)
		radioTab = inactiveTabStyle.Render(radioTab)
		visualizerTab = inactiveTabStyle.Render(visualizerTab)
		settingsTab = activeTabStyle.Render(settingsTab)
	}
	
	// Join tabs with spacing, marking each as a clickable zone.
	tabsLine := lipgloss.JoinHorizontal(lipgloss.Top,
		zone.Mark("tab_library", libraryTab), " ",
		zone.Mark("tab_folder", filesTab), " ",
		zone.Mark("tab_radio", radioTab), " ",
		zone.Mark("tab_visualizer", visualizerTab), " ",
		zone.Mark("tab_settings", settingsTab))

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
	case "visualizer":
		rawContent = m.renderFullScreenVisualizer(availableHeight)
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

	// Height of the rendered now-playing box. renderNowPlaying emits 7 content
	// lines (title, blank, song info, blank, progress bar, blank, controls) plus
	// one line of vertical padding and one border line at the top and bottom:
	// 7 + 2 + 2 = 11. Must stay in sync with renderNowPlaying.
	return 11
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
	controls := []string{"⏮", "⏯", "⏭", "⏹"}
	controlLabels := []string{"Prev", "Play", "Next", "Stop"}
	
	// Adjust play/pause button based on current state
	if m.audioPlayer.IsPlaying() {
		controls[1] = "⏸"
		controlLabels[1] = "Pause"
	} else {
		controls[1] = "▶"
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
		
		// Create button content (marked as a clickable zone)
		buttonContent := fmt.Sprintf("%s %s", control, controlLabels[i])
		controlParts = append(controlParts, zone.Mark(fmt.Sprintf("ctrl_%d", i), buttonStyle.Render(buttonContent)))
	}
	
	controlsLine := strings.Join(controlParts, "  ")
	
	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 40
	}
	
	// lipgloss Width() is the outer box width (border + padding included). With a
	// 1-cell rounded border and 2-cell horizontal padding on each side, the inner
	// content area is totalWidth - 2 (border) - 4 (padding) = totalWidth - 6.
	innerWidth := totalWidth - 6

	// Progress bar
	progressBar := m.renderProgressBar(innerWidth)

	// Create a unified style that handles the entire box
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Width(totalWidth).
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
			displayStr := "⏸  Paused"
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
	
	// Reserve space for visualizer and time display. The rendered line is
	// bar + " " + time + " " + visualizer, i.e. exactly two single-space gaps, so
	// reserving more than that pushes the line past the box's content area.
	visualizerWidth := 24
	timeWidth := len(timeStr)
	reservedSpace := visualizerWidth + timeWidth + 2

	// Calculate progress bar width from the remaining space so the whole line
	// fits exactly within the now-playing box's content area.
	barWidth := width - reservedSpace
	if barWidth < 10 {
		barWidth = 10
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
	
	// Mark the bar itself as a clickable/draggable zone for seeking.
	return zone.Mark("progress", bar) + " " + timeStyle.Render(timeStr) + " " + visualizer
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

// enterLibrarySelection plays the currently selected library item (or drills
// into it). Shared by the Enter key and mouse clicks.
func (m model) enterLibrarySelection() (model, tea.Cmd) {
	if song := m.libraryBrowser.EnterSelected(); song != nil {
		playlist := m.createPlaylistFromContext(song)
		songIndex := m.findSongInPlaylist(playlist, song)
		if songIndex >= 0 {
			m.setPlaylist(playlist, songIndex)
			if m.playCurrentTrack() {
				return m, tickCmd()
			}
		}
	}
	return m, nil
}

// enterFolderSelection opens the currently selected folder entry if it's a
// directory. Shared by the Enter key and mouse clicks.
func (m model) enterFolderSelection() {
	if selected := m.folderBrowser.GetSelected(); selected != "" {
		if m.folderBrowser.IsDirectory(selected) {
			m.folderBrowser.EnterDirectory(selected)
		}
	}
}

// seekFromMouse maps a mouse position within the progress-bar zone to a 0..1
// fraction and seeks there. Returns true if the event landed on the bar.
func (m model) seekFromMouse(msg tea.MouseMsg) bool {
	if m.playingSong == nil || !m.audioPlayer.CanSeek() {
		return false
	}
	z := zone.Get("progress")
	if z == nil || !z.InBounds(msg) {
		return false
	}
	w := z.EndX - z.StartX
	if w <= 0 {
		return false
	}
	relX := msg.Mouse().X - z.StartX
	m.audioPlayer.Seek(float64(relX) / float64(w))
	return true
}

// handleMouseClick routes a left-click to whatever marked zone it lands on:
// tabs, now-playing controls, or a library/folder row. A click on an
// already-selected row activates it (play/open), like pressing Enter.
func (m model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}

	// Tabs switch views.
	for _, t := range []struct{ id, view string }{
		{"tab_library", "library"},
		{"tab_folder", "folder"},
		{"tab_radio", "radio"},
		{"tab_visualizer", "visualizer"},
		{"tab_settings", "settings"},
	} {
		if zone.Get(t.id).InBounds(msg) {
			m.currentView = t.view
			return m, nil
		}
	}

	// Now-playing transport controls.
	if m.playingSong != nil || m.playingStation != nil {
		for i := 0; i < 4; i++ {
			if zone.Get(fmt.Sprintf("ctrl_%d", i)).InBounds(msg) {
				// Activate the clicked control without stealing keyboard focus
				// from the browser.
				m.controlSelected = i
				return m.activateControl()
			}
		}
	}

	// Progress-bar seek (local files only).
	if m.seekFromMouse(msg) {
		return m, nil
	}

	// Content rows.
	switch m.currentView {
	case "library":
		for i := range m.libraryBrowser.GetCategories() {
			if zone.Get(fmt.Sprintf("libcat_%d", i)).InBounds(msg) {
				m.libraryBrowser.SetCategoryIndex(i)
				return m, nil
			}
		}
		for i := range m.libraryBrowser.GetContents() {
			if zone.Get(fmt.Sprintf("libitem_%d", i)).InBounds(msg) {
				already := m.libraryBrowser.GetCurrentPane() == "contents" &&
					m.libraryBrowser.GetContentIndex() == i
				m.libraryBrowser.SetContentIndex(i)
				if already {
					return m.enterLibrarySelection()
				}
				return m, nil
			}
		}
	case "folder":
		for i := range m.folderBrowser.GetEntries() {
			if zone.Get(fmt.Sprintf("folderitem_%d", i)).InBounds(msg) {
				already := m.folderBrowser.GetSelectedIndex() == i
				m.folderBrowser.SetSelectedIndex(i)
				if already {
					m.enterFolderSelection()
				}
				return m, nil
			}
		}
	}

	return m, nil
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
		m.playingStation = nil
		m.radioStartTime = time.Time{}
		m.radioPausedTime = 0
		m.radioWasPaused = false
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
	// Display width, ignoring ANSI escape sequences and correctly counting
	// wide characters (emoji, CJK) as 2 cells.
	return ansi.StringWidth(s)
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
	if maxWidth < 0 {
		maxWidth = 0
	}
	// Display-width-aware truncation that preserves ANSI styling and accounts
	// for wide characters.
	return ansi.Truncate(s, maxWidth, "")
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
		
		line := zone.Mark(fmt.Sprintf("libcat_%d", i), style.Render(prefix+category))
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
	
	// Add breadcrumb if present
	breadcrumb := m.libraryBrowser.GetBreadcrumb()
	if len(breadcrumb) > 0 {
		theme := m.settingsManager.GetTheme()
		breadcrumbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Foreground))
		allLines = append(allLines, breadcrumbStyle.Render("📁 "+strings.Join(breadcrumb, " > ")))
		allLines = append(allLines, "")
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
		
		// Main title line with gradient. Pre-truncate the text to the pane width
		// here so the later truncation in renderLibrary can't cut the trailing
		// zone marker (which would break click detection for long rows).
		mainText := prefix + icon + item.Title
		if visualWidth(mainText) > rightPaneWidth {
			mainText = truncateToWidth(mainText, rightPaneWidth-3) + "..."
		}
		mainStyle := createGradientStyle(selected, rightPaneWidth, theme)
		mainLine := zone.Mark(fmt.Sprintf("libitem_%d", i), mainStyle.Render(mainText))
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
	
	// Use the viewport.top directly from library browser
	// The library browser manages its own viewport positioning
	viewportTop := viewport.top
	
	// Ensure we don't go past the end
	if viewportTop > len(allLines) - viewport.height {
		viewportTop = len(allLines) - viewport.height
	}
	if viewportTop < 0 {
		viewportTop = 0
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
	viewportTop := m.folderBrowser.GetViewportTop()

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

		// Pre-truncate to the pane width so the MaxWidth clip below can't cut the
		// trailing zone marker.
		displayText := prefix + icon + entry
		if visualWidth(displayText) > folderWidth {
			displayText = truncateToWidth(displayText, folderWidth-3) + "..."
		}
		style := createGradientStyle(isSelected, folderWidth, theme)
		// Mark with the absolute entry index so clicks map to the right entry.
		line := zone.Mark(fmt.Sprintf("folderitem_%d", viewportTop+i), style.Render(displayText))
		items = append(items, line)
	}

	// Hard-clip every line to the terminal width so long paths or filenames
	// can't wrap onto a second visual row and push the selection off-screen.
	return lipgloss.NewStyle().MaxWidth(m.width).Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}

// renderSearch draws the search modal floating over a dimmed copy of the app
// (passed as background). The box scales with the terminal — biased toward
// width, since most screens are wider than tall — so long titles aren't clipped
// on big windows.
func (m model) renderSearch(background string) string {
	if !m.searchMode || m.width < 8 || m.height < 6 {
		return background
	}

	theme := m.settingsManager.GetTheme()

	// Responsive box size: ~2/3 width, ~3/5 height, clamped to sane bounds.
	searchBoxWidth := clamp(m.width*2/3, 48, 120)
	if searchBoxWidth > m.width-4 {
		searchBoxWidth = m.width - 4
	}
	searchBoxHeight := clamp(m.height*3/5, 14, 28)
	if searchBoxHeight > m.height-2 {
		searchBoxHeight = m.height - 2
	}

	// Inner content area (Height/Width exclude the border, include padding).
	contentHeight := searchBoxHeight - 2 // top+bottom padding
	innerWidth := searchBoxWidth - 4     // left+right padding (2 each)

	searchBoxStyle := lipgloss.NewStyle().
		Width(searchBoxWidth).
		Height(searchBoxHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2).
		Background(lipgloss.Color(theme.Background))

	// Search input line
	queryStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Foreground)).
		Background(lipgloss.Color(theme.Muted)).
		Padding(0, 1).
		Width(innerWidth)
	queryLine := queryStyle.Render("🔍 Search: " + m.searchQuery + "█")

	tabStrip := m.renderSearchTabs(innerWidth, theme)

	kindIcons := map[string]string{"song": "♪", "artist": "♫", "album": "▤", "genre": "♦"}

	// Header: query, blank, tabs, blank (4 lines). Footer: help (1 line).
	// The rest of the content area is available for result rows.
	var resultLines []string
	resultLines = append(resultLines, queryLine, "", tabStrip, "")
	visible := contentHeight - len(resultLines) - 1 // reserve 1 line for help
	if visible < 3 {
		visible = 3
	}

	maxLineWidth := innerWidth - 2
	if len(m.searchResults) == 0 {
		mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
		if len(m.searchQuery) == 0 {
			resultLines = append(resultLines, mutedStyle.Render("Type to search "+m.searchCategory+"…"))
		} else {
			resultLines = append(resultLines, mutedStyle.Render("No results found"))
		}
	} else {
		// Scroll a window of rows so the selection stays visible.
		start := 0
		if m.searchSelected >= visible {
			start = m.searchSelected - visible + 1
		}
		end := min(start+visible, len(m.searchResults))
		for i := start; i < end; i++ {
			r := m.searchResults[i]
			isSelected := i == m.searchSelected
			prefix := "  "
			if isSelected {
				prefix = "> "
			}

			rowText := prefix + kindIcons[r.kind] + " " + r.title
			if r.subtitle != "" {
				rowText += "  ·  " + r.subtitle
			}
			rowText = ansi.Truncate(rowText, maxLineWidth, "…")

			style := createGradientStyle(isSelected, innerWidth, theme)
			resultLines = append(resultLines, style.Render(rowText))
		}
	}

	// Fill so the help text sits on the last content line.
	for len(resultLines) < contentHeight-1 {
		resultLines = append(resultLines, "")
	}
	helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted)).Italic(true)
	help := "↑↓ move • tab switch • enter play • ^A play all • esc close"
	resultLines = append(resultLines, helpStyle.Render(ansi.Truncate(help, innerWidth, "…")))

	box := searchBoxStyle.Render(strings.Join(resultLines, "\n"))

	// Composite the modal over a dimmed copy of the app using lipgloss layers.
	boxW, boxH := lipgloss.Width(box), lipgloss.Height(box)
	x := (m.width - boxW) / 2
	y := (m.height - boxH) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	canvas := lipgloss.NewCanvas(m.width, m.height)
	canvas.Compose(lipgloss.NewLayer(background)) // paint the app across the canvas
	m.dimCanvas(canvas)                           // fade it toward the background
	// A bare Layer's X/Y offset is only applied by a Compositor (Layer.Draw
	// itself ignores it), so wrap the modal in one to place it at (x, y).
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(box).X(x).Y(y)))
	return canvas.Render()
}

// renderSearchTabs builds the category strip, showing as many tabs as fit in
// width. When they overflow (many categories on a narrow box) it windows around
// the active tab and marks hidden ones with ‹ / ›.
func (m model) renderSearchTabs(width int, theme Theme) string {
	active := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Background)).
		Background(lipgloss.Color(theme.Primary)).
		Bold(true).
		Padding(0, 1)
	inactive := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		Padding(0, 1)

	segs := make([]string, len(searchCategories))
	activeIdx := 0
	for i, c := range searchCategories {
		if c == m.searchCategory {
			segs[i] = active.Render(searchCategoryLabels[c])
			activeIdx = i
		} else {
			segs[i] = inactive.Render(searchCategoryLabels[c])
		}
	}

	if ansi.StringWidth(strings.Join(segs, "")) <= width {
		return strings.Join(segs, "")
	}

	// Grow a window outward from the active tab while it fits (reserve 2 cells
	// for the ‹ › overflow markers).
	lo, hi := activeIdx, activeIdx
	used := ansi.StringWidth(segs[activeIdx])
	for {
		grew := false
		if hi+1 < len(segs) && used+ansi.StringWidth(segs[hi+1]) <= width-2 {
			hi++
			used += ansi.StringWidth(segs[hi])
			grew = true
		}
		if lo-1 >= 0 && used+ansi.StringWidth(segs[lo-1]) <= width-2 {
			lo--
			used += ansi.StringWidth(segs[lo])
			grew = true
		}
		if !grew {
			break
		}
	}
	strip := strings.Join(segs[lo:hi+1], "")
	if lo > 0 {
		strip = "‹" + strip
	}
	if hi < len(segs)-1 {
		strip += "›"
	}
	return strip
}

// dimCanvas fades every already-painted cell toward the theme background so the
// app reads as a lowered-opacity backdrop behind the modal.
func (m model) dimCanvas(c *lipgloss.Canvas) {
	bg := hexToRGB(m.settingsManager.GetTheme().Background)
	for y := 0; y < c.Height(); y++ {
		for x := 0; x < c.Width(); x++ {
			cell := c.CellAt(x, y)
			if cell == nil {
				continue
			}
			cell.Style.Fg = dimColor(cell.Style.Fg, bg, 0.45)
			cell.Style.Bg = dimColor(cell.Style.Bg, bg, 0.30)
		}
	}
}

// dimColor blends c toward bg, keeping `keep` fraction of the original. A nil
// (default) color is left untouched.
func dimColor(c color.Color, bg RGB, keep float64) color.Color {
	if c == nil {
		return nil
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return c
	}
	nr := float64(r>>8)*keep + bg.R*(1-keep)
	ng := float64(g>>8)*keep + bg.G*(1-keep)
	nb := float64(b>>8)*keep + bg.B*(1-keep)
	return color.RGBA{uint8(nr), uint8(ng), uint8(nb), 0xff}
}

// clamp constrains v to [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
			// Reset radio variables when switching to library playback
			m.playingStation = nil
			m.radioStartTime = time.Time{}
			m.radioPausedTime = 0
			m.radioWasPaused = false
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

// searchResult is one row in the Spotlight-style search overlay. Depending on
// the active category it represents a single song or a whole artist/album/genre
// group; songs holds the tracks queued when the row is chosen.
type searchResult struct {
	kind     string // "song", "artist", "album", "genre"
	title    string // primary display text
	subtitle string // secondary line (artist, counts, …)
	song     *Song  // set when kind == "song"
	songs    []Song // tracks queued when this row is selected
	score    int
}

// searchCategories is the Tab-cycle order for the search overlay. The last two
// match the query against a song's genre: "genre-songs" lists matching songs,
// "genre-albums" lists albums whose genre matches.
var searchCategories = []string{"songs", "artists", "albums", "genres", "genre-songs", "genre-albums"}

// searchCategoryLabels are the display names for the tab strip.
var searchCategoryLabels = map[string]string{
	"songs":        "Songs",
	"artists":      "Artists",
	"albums":       "Albums",
	"genres":       "Genres",
	"genre-songs":  "Genre songs",
	"genre-albums": "Genre albums",
}

// groupKey returns the bucket key for a song under the given grouping ("artist",
// "album", "genre"). The album key mirrors LibraryBrowser.getAlbums
// ("Album - Artist"), and empty tags fall back to the same "Unknown …" labels
// the library browser shows.
func groupKey(s Song, kind string) string {
	switch kind {
	case "artist":
		if s.Artist == "" {
			return "Unknown Artist"
		}
		return s.Artist
	case "album":
		album := s.Album
		if album == "" {
			album = "Unknown Album"
		}
		return album + " - " + s.Artist
	case "genre":
		if s.Genre == "" {
			return "Unknown Genre"
		}
		return s.Genre
	}
	return ""
}

// groupLabel is the display title (and fuzzy-match target) for a group key: the
// bare album title for albums, the key itself for artists/genres.
func groupLabel(kind, key string) string {
	if kind == "album" {
		album, _ := splitAlbumKey(key)
		return album
	}
	return key
}

func (m *model) closeSearch() {
	m.searchMode = false
	m.searchQuery = ""
	m.searchResults = nil
	m.searchSelected = 0
}

// cycleSearchCategory moves the active category forward (dir=1) or backward
// (dir=-1) through searchCategories and re-runs the query.
func (m *model) cycleSearchCategory(dir int) {
	idx := 0
	for i, c := range searchCategories {
		if c == m.searchCategory {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(searchCategories)) % len(searchCategories)
	m.searchCategory = searchCategories[idx]
	m.performSearch(m.searchQuery)
}

// playSelectedSearchResult builds a queue from the highlighted row and starts
// playback. A song row queues the visible songs (starting at the selection); an
// artist/album/genre row queues that whole group. Returns false if nothing plays.
func (m *model) playSelectedSearchResult() bool {
	if m.searchSelected < 0 || m.searchSelected >= len(m.searchResults) {
		return false
	}
	sel := m.searchResults[m.searchSelected]

	var queue []Song
	start := 0
	if sel.kind == "song" {
		for i, r := range m.searchResults {
			if r.kind != "song" || r.song == nil {
				continue
			}
			if i == m.searchSelected {
				start = len(queue)
			}
			queue = append(queue, *r.song)
		}
	} else {
		queue = append(queue, sel.songs...)
	}

	if len(queue) == 0 {
		return false
	}
	m.setPlaylist(queue, start)
	return m.playCurrentTrack()
}

// playAllSearchResults queues every song across all current results (deduped by
// file path, in listed order) and starts playing — the "play all shown" action.
func (m *model) playAllSearchResults() bool {
	seen := make(map[string]bool)
	var queue []Song
	for _, r := range m.searchResults {
		for _, s := range r.songs {
			if !seen[s.FilePath] {
				seen[s.FilePath] = true
				queue = append(queue, s)
			}
		}
	}
	if len(queue) == 0 {
		return false
	}
	m.setPlaylist(queue, 0)
	return m.playCurrentTrack()
}

// performSearch scores every song against the query (across title/artist/album)
// and then presents the hits in the active category. In a grouped category the
// query filters songs and we group the survivors by that dimension — so typing
// an artist name and Tab-ing to Albums lists that artist's albums, not albums
// whose title happens to match. A group also appears if its own label matches
// (e.g. "jazz" in Genres), in which case the whole group is queued.
func (m *model) performSearch(query string) {
	m.searchSelected = 0
	m.searchResults = nil
	if len(query) == 0 {
		return
	}

	q := strings.ToLower(query)
	allSongs := m.libraryManager.GetSongs()

	// The genre-scoped categories match a song's genre; the rest match
	// title/artist/album. Score each song once with the right target.
	byGenre := m.searchCategory == "genre-songs" || m.searchCategory == "genre-albums"
	scores := make([]int, len(allSongs))
	for i := range allSongs {
		if byGenre {
			scores[i] = fuzzyScore(allSongs[i].Genre, q)
		} else {
			scores[i] = scoreSong(allSongs[i], q)
		}
	}

	// Flat categories that produce one row per matching song.
	if m.searchCategory == "songs" || m.searchCategory == "genre-songs" {
		var results []searchResult
		for i := range allSongs {
			if scores[i] > 0 {
				s := allSongs[i]
				subtitle := songSubtitle(s)
				if byGenre {
					subtitle = s.Artist + " • " + s.Genre
				}
				results = append(results, searchResult{
					kind:     "song",
					title:    s.Title,
					subtitle: subtitle,
					song:     &s,
					songs:    []Song{s},
					score:    scores[i],
				})
			}
		}
		m.searchResults = sortTrim(results)
		return
	}

	// Grouped categories: bucket every song, tracking which ones matched.
	// "genre-albums" groups by album but was matched on genre above.
	kind := "album"
	switch m.searchCategory {
	case "artists":
		kind = "artist"
	case "genres":
		kind = "genre"
	}
	type bucket struct {
		all, matched []Song
		best         int
	}
	buckets := make(map[string]*bucket)
	for i := range allSongs {
		key := groupKey(allSongs[i], kind)
		b := buckets[key]
		if b == nil {
			b = &bucket{}
			buckets[key] = b
		}
		b.all = append(b.all, allSongs[i])
		if scores[i] > b.best {
			b.best = scores[i]
		}
		if scores[i] > 0 {
			b.matched = append(b.matched, allSongs[i])
		}
	}

	var results []searchResult
	for key, b := range buckets {
		score := b.best
		// A group can also surface if its own label matches (e.g. "jazz" in
		// Genres). Genre-albums are matched purely on the genre field above.
		if !byGenre {
			if ls := fuzzyScore(groupLabel(kind, key), q); ls > score {
				score = ls
			}
		}
		if score <= 0 {
			continue
		}

		// Queue the matching songs; if only the label matched, queue everything.
		songs := b.matched
		if len(songs) == 0 {
			songs = b.all
		}

		r := searchResult{kind: kind, title: groupLabel(kind, key), songs: songs, score: score}
		switch kind {
		case "artist":
			r.subtitle = groupSubtitle(songs, "artist")
		case "album":
			sortByTrack(songs)
			_, artist := splitAlbumKey(key)
			r.subtitle = artist + " • " + strconv.Itoa(len(songs)) + " songs"
			if m.searchCategory == "genre-albums" && len(songs) > 0 {
				r.subtitle = artist + " • " + songs[0].Genre + " • " + strconv.Itoa(len(songs)) + " songs"
			}
		case "genre":
			r.subtitle = groupSubtitle(songs, "genre")
		}
		results = append(results, r)
	}
	m.searchResults = sortTrim(results)
}

// sortTrim orders results by score (title as a stable tie-break) and caps the list.
func sortTrim(results []searchResult) []searchResult {
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].title < results[j].title
	})
	if len(results) > 50 {
		results = results[:50]
	}
	return results
}

// scoreSong ranks a song against the query across its fields, weighting the
// title above the artist above the album. Returns 0 when nothing matches.
func scoreSong(s Song, query string) int {
	best := 0
	if v := fuzzyScore(s.Title, query); v > 0 {
		best = max(best, v+50)
	}
	if v := fuzzyScore(s.Artist, query); v > 0 {
		best = max(best, v+20)
	}
	if v := fuzzyScore(s.Album, query); v > 0 {
		best = max(best, v)
	}
	return best
}

// fuzzyScore ranks how well query (already lowercased by the caller) matches
// text. It returns 0 when query is not a subsequence of text. Higher is better:
// exact and prefix matches dominate, then contiguous substrings, then
// word-boundary starts, then loose subsequence matches with an adjacency bonus.
func fuzzyScore(text, query string) int {
	if query == "" {
		return 0
	}
	lt := strings.ToLower(text)

	if lt == query {
		return 1000
	}
	if strings.HasPrefix(lt, query) {
		return 800 + lengthBonus(lt)
	}
	if idx := strings.Index(lt, query); idx >= 0 {
		score := 500
		if isBoundary(lt, idx) {
			score += 100
		}
		return score + lengthBonus(lt)
	}

	// Loose subsequence match with adjacency / word-boundary bonuses.
	ti, qi, score := 0, 0, 0
	prevMatched := false
	for ti < len(lt) && qi < len(query) {
		if lt[ti] == query[qi] {
			score++
			if prevMatched {
				score += 5
			}
			if isBoundary(lt, ti) {
				score += 3
			}
			qi++
			prevMatched = true
		} else {
			prevMatched = false
		}
		ti++
	}
	if qi < len(query) {
		return 0 // not all query characters matched
	}
	return score
}

// isBoundary reports whether position i in text begins a word.
func isBoundary(text string, i int) bool {
	return i == 0 || text[i-1] == ' ' || text[i-1] == '-' || text[i-1] == '_'
}

// lengthBonus favours shorter (usually more relevant) matches.
func lengthBonus(text string) int {
	if len(text) >= 40 {
		return 0
	}
	return 40 - len(text)
}

func songSubtitle(s Song) string {
	subtitle := s.Artist
	if s.Album != "" && s.Album != "Unknown Album" {
		if subtitle != "" {
			subtitle += " • "
		}
		subtitle += s.Album
	}
	return subtitle
}

// groupSubtitle builds the "N albums, M songs" / "N artists, M songs" count line
// for an artist or genre group, matching LibraryBrowser's wording.
func groupSubtitle(songs []Song, kind string) string {
	seen := make(map[string]bool)
	var noun string
	for _, s := range songs {
		if kind == "artist" {
			key := s.Album
			if key == "" {
				key = "Unknown Album"
			}
			seen[key] = true
			noun = "album"
		} else { // genre → count artists
			key := s.Artist
			if key == "" {
				key = "Unknown Artist"
			}
			seen[key] = true
			noun = "artist"
		}
	}
	word := noun + "s"
	if len(seen) == 1 {
		word = noun
	}
	return fmt.Sprintf("%d %s, %d songs", len(seen), word, len(songs))
}

func splitAlbumKey(key string) (album, artist string) {
	parts := strings.SplitN(key, " - ", 2)
	album = parts[0]
	if len(parts) > 1 {
		artist = parts[1]
	}
	if artist == "" {
		artist = "Unknown Artist"
	}
	return album, artist
}

func sortByTrack(songs []Song) {
	sort.SliceStable(songs, func(i, j int) bool {
		return songs[i].TrackNumber < songs[j].TrackNumber
	})
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
	
	items = append(items, headerStyle.Render("⚠  Clear Music Library"))
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
	
	items = append(items, headerStyle.Render("⚠  Clear Radio Library"))
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


// renderVisualizer renders the music visualizer using real audio data
func (m *model) renderVisualizer(width int) string {
	isPlaying := m.audioPlayer.IsPlaying()
	theme := m.settingsManager.GetTheme()
	
	if !isPlaying {
		// Show flat line when not playing with muted color
		mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
		return mutedStyle.Render(strings.Repeat("▁", width))
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
		
		// Color the bar based on frequency range using harmonious theme colors
		var style lipgloss.Style
		intensity := amplitude
		
		if i < numBars/4 {
			// Bass frequencies - use primary colors and variations
			if intensity > 0.7 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
			} else if intensity > 0.4 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary))
			} else {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
			}
		} else if i < numBars/2 {
			// Mid frequencies - use secondary and primary colors
			if intensity > 0.7 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary))
			} else if intensity > 0.4 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
			} else {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
			}
		} else {
			// High frequencies - use gradient colors for harmony
			if intensity > 0.7 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientStart))
			} else if intensity > 0.4 {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientEnd))
			} else {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
			}
		}
		
		bars = append(bars, style.Render(barChar))
	}
	
	return strings.Join(bars, "")
}

// renderFullScreenVisualizer renders the full-screen music visualizer
func (m model) renderFullScreenVisualizer(availableHeight int) string {
	theme := m.settingsManager.GetTheme()
	isPlaying := m.audioPlayer.IsPlaying()
	
	var content []string
	
	// Header with chart type indicator
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true).
		Align(lipgloss.Center)
	
	title := "Music Visualizer"
	if isPlaying {
		if m.playingSong != nil {
			title = fmt.Sprintf("♪ %s - %s", m.playingSong.Title, m.playingSong.Artist)
		} else if m.playingStation != nil {
			title = fmt.Sprintf("♪ %s", m.playingStation.Name)
		}
	} else {
		title = "Music Visualizer (No audio playing)"
	}
	
	// Add chart type indicator
	chartTypeNames := map[string]string{
		"unicode": "Unicode Blocks",
		"bars":    "Bar Chart",
		"line":    "Line Chart",
		"wave":    "Wave Chart",
	}
	
	chartName := chartTypeNames[m.currentChartType]
	if chartName == "" {
		chartName = "Unknown"
	}
	
	content = append(content, headerStyle.Render(title))
	content = append(content, lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		Align(lipgloss.Center).
		Render(fmt.Sprintf("[ %s ] Use ← → to switch chart types", chartName)))
	content = append(content, "")
	
	// Calculate available space for visualizer
	// Reserve space for header (3 lines), controls (2 lines), and some padding
	visualizerHeight := availableHeight - 5
	
	// Ensure minimum height
	if visualizerHeight < 1 {
		visualizerHeight = 1
	}
	
	// Ensure maximum height for reasonable performance
	if visualizerHeight > 20 {
		visualizerHeight = 20
	}
	
	// Calculate visualizer dimensions
	visualizerWidth := m.width - 4
	if visualizerWidth > 80 {
		visualizerWidth = 80
	}
	if visualizerWidth < 20 {
		visualizerWidth = 20
	}
	
	// Get real audio samples from the audio player
	audioSamples := m.audioPlayer.GetAudioSamples()
	
	// Render based on current chart type
	var visualizerContent string
	switch m.currentChartType {
	case "unicode":
		visualizerContent = m.renderUnicodeVisualizer(audioSamples, visualizerWidth, visualizerHeight, isPlaying, theme)
	case "bars":
		visualizerContent = m.renderBarChart(audioSamples, visualizerWidth, visualizerHeight, isPlaying, theme)
	case "line":
		visualizerContent = m.renderLineChart(audioSamples, visualizerWidth, visualizerHeight, isPlaying, theme)
	case "wave":
		visualizerContent = m.renderWaveChart(audioSamples, visualizerWidth, visualizerHeight, isPlaying, theme)
	default:
		visualizerContent = m.renderUnicodeVisualizer(audioSamples, visualizerWidth, visualizerHeight, isPlaying, theme)
	}
	
	content = append(content, visualizerContent)
	
	// Add controls/info
	controlsStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Muted)).
		Align(lipgloss.Center)
	
	var controlsText string
	if isPlaying {
		controlsText = "Press 'space' to pause • Press 'f' to switch tabs • Use ← → to switch chart types"
	} else {
		controlsText = "No audio playing • Press 'f' to switch tabs • Use ← → to switch chart types"
	}
	
	content = append(content, "")
	content = append(content, controlsStyle.Render(controlsText))
	
	return strings.Join(content, "\n")
}

// renderUnicodeVisualizer renders the high-resolution Unicode block visualizer
func (m model) renderUnicodeVisualizer(audioSamples []float64, width, height int, isPlaying bool, theme Theme) string {
	var content []string
	
	// Create high-resolution visualizer using Unicode block characters
	blockChars := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	
	for row := 0; row < height; row++ {
		var bars []string
		
		for i := 0; i < width; i++ {
			var amplitude float64
			
			if isPlaying && len(audioSamples) > 0 {
				// Use interpolation for smoother visualization
				sampleIndex := (i * len(audioSamples)) / width
				if sampleIndex >= len(audioSamples) {
					sampleIndex = len(audioSamples) - 1
				}
				
				// Get base amplitude
				amplitude = audioSamples[sampleIndex]
				
				// Add some neighboring samples for smoothing
				if len(audioSamples) > 3 {
					neighbors := 0
					neighborSum := amplitude
					
					if sampleIndex > 0 {
						neighborSum += audioSamples[sampleIndex-1]
						neighbors++
					}
					if sampleIndex < len(audioSamples)-1 {
						neighborSum += audioSamples[sampleIndex+1]
						neighbors++
					}
					
					amplitude = neighborSum / float64(neighbors+1)
				}
				
				// Apply more nuanced amplitude scaling for better detail
				// Use a gentler amplification to avoid hitting max values too easily
				amplitude *= 2.5
				
				// Apply logarithmic scaling to preserve detail at different levels
				if amplitude > 0.1 {
					amplitude = 0.1 + (amplitude-0.1)*0.7 // Compress higher values
				}
				
				// Final clamp to ensure we don't exceed 1.0
				if amplitude > 1.0 {
					amplitude = 1.0
				}
			} else {
				// Show minimal activity when not playing
				amplitude = 0.02
			}
			
			// Convert amplitude to total height in "sub-characters" (8 levels per row)
			totalSubHeight := amplitude * float64(height * 8)
			
			// Calculate what should be shown in this row
			rowStartHeight := float64((height - row - 1) * 8)
			rowEndHeight := float64((height - row) * 8)
			
			var barChar string
			if totalSubHeight <= rowStartHeight {
				// Bar doesn't reach this row
				barChar = " "
			} else if totalSubHeight >= rowEndHeight {
				// Bar fills this entire row
				barChar = "█"
			} else {
				// Bar partially fills this row - calculate sub-character level
				subLevel := int(totalSubHeight - rowStartHeight)
				if subLevel > 8 {
					subLevel = 8
				}
				if subLevel < 0 {
					subLevel = 0
				}
				barChar = blockChars[subLevel]
			}
			
			// Color the bar based on frequency range using harmonious theme colors
			var style lipgloss.Style
			intensity := amplitude
			
			if i < width/4 {
				// Bass frequencies - use primary colors and variations
				if intensity > 0.5 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
				} else if intensity > 0.2 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary))
				} else {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
				}
			} else if i < width/2 {
				// Mid frequencies - use secondary and primary colors
				if intensity > 0.5 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary))
				} else if intensity > 0.2 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
				} else {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
				}
			} else if i < 3*width/4 {
				// High-mid frequencies - use gradient colors for harmony
				if intensity > 0.5 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientStart))
				} else if intensity > 0.2 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientEnd))
				} else {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
				}
			} else {
				// Treble frequencies - use gradient colors with primary
				if intensity > 0.5 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientEnd))
				} else if intensity > 0.2 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientStart))
				} else {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Muted))
				}
			}
			
			bars = append(bars, style.Render(barChar))
		}
		
		// Center the visualizer row
		rowContent := strings.Join(bars, "")
		paddingNeeded := (m.width - width) / 2
		if paddingNeeded > 0 {
			rowContent = strings.Repeat(" ", paddingNeeded) + rowContent
		}
		
		content = append(content, rowContent)
	}
	
	return strings.Join(content, "\n")
}

// renderBarChart renders audio data using ntcharts bar chart
func (m model) renderBarChart(audioSamples []float64, width, height int, isPlaying bool, theme Theme) string {
	if !isPlaying || len(audioSamples) == 0 {
		// Create empty bar chart
		bc := barchart.New(width, height)
		bc.Draw()
		chartView := bc.View()
		
		// Center the chart
		return m.centerChartContent(chartView, width)
	}
	
	// Create bar chart with audio data
	bc := barchart.New(width, height)
	
	// Process audio samples into bar data
	numBars := min(len(audioSamples), width/2) // Limit bars to fit
	var barData []barchart.BarData
	
	for i := 0; i < numBars; i++ {
		amplitude := audioSamples[i]
		
		// Amplify for better visualization
		amplitude *= 100
		if amplitude > 100 {
			amplitude = 100
		}
		
		// Color based on frequency range using harmonious theme colors
		var style lipgloss.Style
		if i < numBars/4 {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)) // Bass
		} else if i < numBars/2 {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)) // Mid
		} else if i < 3*numBars/4 {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientStart)) // High-mid
		} else {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientEnd)) // Treble
		}
		
		barData = append(barData, barchart.BarData{
			Label: fmt.Sprintf("%d", i),
			Values: []barchart.BarValue{
				{Name: fmt.Sprintf("%.0f", amplitude), Value: amplitude, Style: style},
			},
		})
	}
	
	bc.PushAll(barData)
	bc.Draw()
	
	chartView := bc.View()
	
	// Center the chart
	return m.centerChartContent(chartView, width)
}

// renderLineChart renders audio data using ntcharts line chart
func (m model) renderLineChart(audioSamples []float64, width, height int, isPlaying bool, theme Theme) string {
	if !isPlaying || len(audioSamples) == 0 {
		// Return empty space
		var content []string
		for i := 0; i < height; i++ {
			content = append(content, strings.Repeat(" ", width))
		}
		return strings.Join(content, "\n")
	}
	
	// Create a simple line chart representation
	var content []string
	
	// Process samples to fit width with proper amplification
	processedSamples := make([]float64, width)
	for i := 0; i < width; i++ {
		sampleIndex := (i * len(audioSamples)) / width
		if sampleIndex >= len(audioSamples) {
			sampleIndex = len(audioSamples) - 1
		}
		
		amplitude := audioSamples[sampleIndex]
		
		// Apply amplification for better visibility (more aggressive than other visualizers)
		amplitude *= 4.0
		
		// Clamp to reasonable range
		if amplitude > 1.0 {
			amplitude = 1.0
		}
		if amplitude < 0.0 {
			amplitude = 0.0
		}
		
		processedSamples[i] = amplitude * float64(height)
	}
	
	// Create line visualization
	for row := 0; row < height; row++ {
		var line []string
		for i := 0; i < width; i++ {
			barHeight := int(processedSamples[i])
			if barHeight >= (height - row) {
				// Color based on position using harmonious theme colors
				var style lipgloss.Style
				if i < width/4 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)) // Bass
				} else if i < width/2 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Secondary)) // Mid
				} else if i < 3*width/4 {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientStart)) // High-mid
				} else {
					style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.GradientEnd)) // Treble
				}
				line = append(line, style.Render("●"))
			} else {
				line = append(line, " ")
			}
		}
		
		// Center the line
		lineContent := strings.Join(line, "")
		paddingNeeded := (m.width - width) / 2
		if paddingNeeded > 0 {
			lineContent = strings.Repeat(" ", paddingNeeded) + lineContent
		}
		
		content = append(content, lineContent)
	}
	
	return strings.Join(content, "\n")
}

// renderWaveChart renders audio data using ntcharts wave chart
func (m model) renderWaveChart(audioSamples []float64, width, height int, isPlaying bool, theme Theme) string {
	if !isPlaying || len(audioSamples) == 0 {
		// Create empty wave chart with Y-axis starting from 0
		wlc := wavelinechart.New(width, height, 
			wavelinechart.WithYRange(0, 1),
			wavelinechart.WithStyles(runes.ArcLineStyle, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))))
		wlc.Draw()
		chartView := wlc.View()
		
		// Center the chart
		return m.centerChartContent(chartView, width)
	}
	
	// Create wave chart with audio data, Y-axis starting from 0
	wlc := wavelinechart.New(width, height, 
		wavelinechart.WithYRange(0, 1),
		wavelinechart.WithStyles(runes.ArcLineStyle, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))))
	
	// To create a more wave-like appearance, we need to add more points for smoother curves
	// Use interpolation to create additional points between samples
	numPoints := min(len(audioSamples), width/2) // Use fewer samples but interpolate
	
	for i := 0; i < numPoints; i++ {
		sampleIndex := (i * len(audioSamples)) / numPoints
		if sampleIndex >= len(audioSamples) {
			sampleIndex = len(audioSamples) - 1
		}
		
		amplitude := audioSamples[sampleIndex]
		
		// Ensure amplitude is positive (since we're starting from 0)
		if amplitude < 0 {
			amplitude = -amplitude // Take absolute value
		}
		
		// Smooth the amplitude with neighboring samples for wave-like appearance
		if len(audioSamples) > 3 && sampleIndex > 0 && sampleIndex < len(audioSamples)-1 {
			prevAmp := audioSamples[sampleIndex-1]
			nextAmp := audioSamples[sampleIndex+1]
			if prevAmp < 0 { prevAmp = -prevAmp }
			if nextAmp < 0 { nextAmp = -nextAmp }
			
			// Apply smoothing
			amplitude = (prevAmp * 0.25 + amplitude * 0.5 + nextAmp * 0.25)
		}
		
		// Amplify for better visibility
		amplitude *= 2.0
		
		// Clamp to range [0, 1]
		if amplitude > 1.0 {
			amplitude = 1.0
		}
		if amplitude < 0.0 {
			amplitude = 0.0
		}
		
		// Plot multiple points to create smoother wave curves
		xPos := float64(i * width) / float64(numPoints)
		wlc.Plot(canvas.Float64Point{X: xPos, Y: amplitude})
		
		// Add interpolated points between samples for smoother curves
		if i < numPoints-1 {
			nextSampleIndex := ((i + 1) * len(audioSamples)) / numPoints
			if nextSampleIndex < len(audioSamples) {
				nextAmplitude := audioSamples[nextSampleIndex]
				if nextAmplitude < 0 { nextAmplitude = -nextAmplitude }
				nextAmplitude *= 2.0
				if nextAmplitude > 1.0 { nextAmplitude = 1.0 }
				
				// Add intermediate points for smooth curves
				for j := 1; j < 4; j++ {
					interpFactor := float64(j) / 4.0
					interpAmplitude := amplitude + (nextAmplitude - amplitude) * interpFactor
					interpX := xPos + (float64(width) / float64(numPoints)) * interpFactor
					wlc.Plot(canvas.Float64Point{X: interpX, Y: interpAmplitude})
				}
			}
		}
	}
	
	wlc.Draw()
	chartView := wlc.View()
	
	// Center the chart
	return m.centerChartContent(chartView, width)
}


// centerChartContent centers chart content on the screen
func (m model) centerChartContent(chartView string, chartWidth int) string {
	lines := strings.Split(chartView, "\n")
	var centeredLines []string
	
	for _, line := range lines {
		// Calculate padding needed to center the line
		paddingNeeded := (m.width - chartWidth) / 2
		if paddingNeeded > 0 {
			centeredLine := strings.Repeat(" ", paddingNeeded) + line
			centeredLines = append(centeredLines, centeredLine)
		} else {
			centeredLines = append(centeredLines, line)
		}
	}
	
	return strings.Join(centeredLines, "\n")
}


func main() {
	zone.NewGlobal() // initialize the mouse-zone manager for clickable elements
	m := initialModel()
	defer m.audioPlayer.Close()

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}
}