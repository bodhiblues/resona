package main

import (
	"fmt"
	"strings"
)

// RadioBrowser handles the radio station browsing interface
type RadioBrowser struct {
	radioLibrary  *RadioLibrary
	currentView   string // "list", "add", "edit", "quickadd"
	stations      []RadioStation
	selected      int
	viewport      viewport
	// Quick add fields
	quickURL      string
	quickName     string
	// Add station form fields
	formName        string
	formURL         string
	formGenre       string
	formLanguage    string
	formCountry     string
	formDescription string
	formTags        string
	formField       int // 0=name, 1=url, 2=genre, 3=language, 4=country, 5=description, 6=tags
	// Input state
	inputMode       bool
	inputBuffer     string
}

// NewRadioBrowser creates a new radio browser instance
func NewRadioBrowser(radioLibrary *RadioLibrary) *RadioBrowser {
	rb := &RadioBrowser{
		radioLibrary: radioLibrary,
		currentView:  "list",
		stations:     radioLibrary.GetStations(),
		selected:     0,
		viewport:     viewport{top: 0, height: 20},
		formField:    0,
		inputMode:    false,
	}
	
	// If no stations exist, show quick add modal
	if len(rb.stations) == 0 {
		rb.showQuickAdd()
	}
	
	return rb
}

// showQuickAdd switches to the quick add modal
func (rb *RadioBrowser) showQuickAdd() {
	rb.currentView = "quickadd"
	rb.quickURL = "https://somafm.com/groovesalad256.pls"
	rb.quickName = ""
	rb.inputMode = false
	rb.inputBuffer = ""
}

// showAddForm switches to the add station form
func (rb *RadioBrowser) showAddForm() {
	rb.currentView = "add"
	rb.formName = ""
	rb.formURL = "https://somafm.com/groovesalad256.pls"
	rb.formGenre = ""
	rb.formLanguage = ""
	rb.formCountry = ""
	rb.formDescription = ""
	rb.formTags = ""
	rb.formField = 0
	rb.inputMode = false
	rb.inputBuffer = ""
}

// MoveUp moves selection up
func (rb *RadioBrowser) MoveUp() {
	if rb.currentView == "list" {
		if rb.selected > 0 {
			rb.selected--
			rb.adjustViewport()
		}
	} else if rb.currentView == "add" {
		if rb.formField > 0 {
			rb.formField--
		}
	} else if rb.currentView == "quickadd" {
		// Toggle between URL and name fields
		if rb.formField == 1 {
			rb.formField = 0
		}
	}
}

// MoveDown moves selection down
func (rb *RadioBrowser) MoveDown() {
	if rb.currentView == "list" {
		if rb.selected < len(rb.stations)-1 {
			rb.selected++
			rb.adjustViewport()
		}
	} else if rb.currentView == "add" {
		if rb.formField < 6 {
			rb.formField++
		}
	} else if rb.currentView == "quickadd" {
		// Toggle between URL and name fields
		if rb.formField == 0 {
			rb.formField = 1
		}
	}
}

// adjustViewport keeps selected item visible
func (rb *RadioBrowser) adjustViewport() {
	if rb.selected < rb.viewport.top {
		rb.viewport.top = rb.selected
	} else if rb.selected >= rb.viewport.top+rb.viewport.height {
		rb.viewport.top = rb.selected - rb.viewport.height + 1
	}
	
	if rb.viewport.top < 0 {
		rb.viewport.top = 0
	}
}

// SetViewportHeight sets the viewport height
func (rb *RadioBrowser) SetViewportHeight(height int) {
	rb.viewport.height = height
	rb.adjustViewport()
}

// EnterSelected handles enter key press
func (rb *RadioBrowser) EnterSelected() *RadioStation {
	if rb.currentView == "list" {
		if rb.selected < len(rb.stations) {
			station := &rb.stations[rb.selected]
			rb.radioLibrary.UpdateLastPlayed(station.Name)
			return station
		}
	}
	return nil
}

// StartInput starts input mode for current form field
func (rb *RadioBrowser) StartInput() {
	if rb.currentView == "add" {
		rb.inputMode = true
		rb.inputBuffer = rb.getCurrentFieldValue()
	}
}

// getCurrentFieldValue returns the current value of the selected form field
func (rb *RadioBrowser) getCurrentFieldValue() string {
	if rb.currentView == "quickadd" {
		switch rb.formField {
		case 0:
			return rb.quickURL
		case 1:
			return rb.quickName
		}
	} else {
		switch rb.formField {
		case 0:
			return rb.formName
		case 1:
			return rb.formURL
		case 2:
			return rb.formGenre
		case 3:
			return rb.formLanguage
		case 4:
			return rb.formCountry
		case 5:
			return rb.formDescription
		case 6:
			return rb.formTags
		}
	}
	return ""
}

// AddInputChar adds a character to the input buffer
func (rb *RadioBrowser) AddInputChar(ch rune) {
	if rb.inputMode {
		rb.inputBuffer += string(ch)
	}
}

// RemoveInputChar removes the last character from input buffer
func (rb *RadioBrowser) RemoveInputChar() {
	if rb.inputMode && len(rb.inputBuffer) > 0 {
		rb.inputBuffer = rb.inputBuffer[:len(rb.inputBuffer)-1]
	}
}

// FinishInput completes input and updates the form field
func (rb *RadioBrowser) FinishInput() {
	if rb.inputMode {
		if rb.currentView == "quickadd" {
			switch rb.formField {
			case 0:
				rb.quickURL = rb.inputBuffer
			case 1:
				rb.quickName = rb.inputBuffer
			}
		} else {
			switch rb.formField {
			case 0:
				rb.formName = rb.inputBuffer
			case 1:
				rb.formURL = rb.inputBuffer
			case 2:
				rb.formGenre = rb.inputBuffer
			case 3:
				rb.formLanguage = rb.inputBuffer
			case 4:
				rb.formCountry = rb.inputBuffer
			case 5:
				rb.formDescription = rb.inputBuffer
			case 6:
				rb.formTags = rb.inputBuffer
			}
		}
		rb.inputMode = false
		rb.inputBuffer = ""
	}
}

// CancelInput cancels input mode
func (rb *RadioBrowser) CancelInput() {
	rb.inputMode = false
	rb.inputBuffer = ""
}

// SaveStation saves the current form as a new station
func (rb *RadioBrowser) SaveStation() error {
	if rb.formName == "" {
		return fmt.Errorf("station name is required")
	}
	if rb.formURL == "" {
		return fmt.Errorf("station URL is required")
	}
	
	// Parse tags
	var tags []string
	if rb.formTags != "" {
		for _, tag := range strings.Split(rb.formTags, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	
	station := RadioStation{
		Name:        rb.formName,
		URL:         rb.formURL,
		Genre:       rb.formGenre,
		Language:    rb.formLanguage,
		Country:     rb.formCountry,
		Description: rb.formDescription,
		Tags:        tags,
		Metadata:    make(map[string]string),
	}
	
	if err := rb.radioLibrary.AddStation(station); err != nil {
		return fmt.Errorf("failed to add station: %w", err)
	}
	
	// Refresh station list and return to list view
	rb.stations = rb.radioLibrary.GetStations()
	rb.currentView = "list"
	rb.selected = len(rb.stations) - 1 // Select the newly added station
	rb.adjustViewport()
	
	return nil
}

// PlayQuickStation plays the quick add URL immediately without saving
func (rb *RadioBrowser) PlayQuickStation() (*RadioStation, error) {
	if rb.quickURL == "" {
		return nil, fmt.Errorf("URL is required")
	}
	
	// Create a temporary station for immediate play
	name := rb.quickName
	if name == "" {
		name = "Quick Station"
	}
	
	station := &RadioStation{
		Name: name,
		URL:  rb.quickURL,
	}
	
	// Resolve stream URL if it's a playlist
	if strings.Contains(station.URL, ".pls") || strings.Contains(station.URL, ".m3u") {
		streamURLs, err := resolvePlaylistURL(station.URL)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve playlist URL: %w", err)
		}
		station.StreamURLs = streamURLs
		station.StreamURL = streamURLs[0] // Set primary URL for backward compatibility
	} else {
		station.StreamURL = station.URL
		station.StreamURLs = []string{station.URL} // Single URL as array
	}
	
	return station, nil
}

// SaveQuickStation saves the quick add station to the library
func (rb *RadioBrowser) SaveQuickStation() error {
	if rb.quickURL == "" {
		return fmt.Errorf("URL is required")
	}
	
	name := rb.quickName
	if name == "" {
		name = "Quick Station"
	}
	
	station := RadioStation{
		Name:     name,
		URL:      rb.quickURL,
		Metadata: make(map[string]string),
	}
	
	if err := rb.radioLibrary.AddStation(station); err != nil {
		return fmt.Errorf("failed to add station: %w", err)
	}
	
	// Refresh station list and return to list view
	rb.stations = rb.radioLibrary.GetStations()
	rb.currentView = "list"
	rb.selected = len(rb.stations) - 1 // Select the newly added station
	rb.adjustViewport()
	
	return nil
}

// CancelQuickAdd cancels quick add and returns to list view (or shows message if no stations)
func (rb *RadioBrowser) CancelQuickAdd() {
	rb.currentView = "list"
	rb.stations = rb.radioLibrary.GetStations()
	if len(rb.stations) > 0 {
		rb.selected = 0
		rb.adjustViewport()
	}
}

// CancelAdd cancels adding a station and returns to list view
func (rb *RadioBrowser) CancelAdd() {
	rb.currentView = "list"
	rb.stations = rb.radioLibrary.GetStations()
	if len(rb.stations) > 0 {
		rb.selected = 0
		rb.adjustViewport()
	}
}

// GetCurrentView returns the current view
func (rb *RadioBrowser) GetCurrentView() string {
	return rb.currentView
}

// GetStations returns current stations
func (rb *RadioBrowser) GetStations() []RadioStation {
	return rb.stations
}

// GetSelected returns current selection
func (rb *RadioBrowser) GetSelected() int {
	return rb.selected
}

// GetViewport returns current viewport
func (rb *RadioBrowser) GetViewport() viewport {
	return rb.viewport
}

// GetFormField returns current form field
func (rb *RadioBrowser) GetFormField() int {
	return rb.formField
}

// GetFormData returns current form data
func (rb *RadioBrowser) GetFormData() (string, string, string, string, string, string, string) {
	return rb.formName, rb.formURL, rb.formGenre, rb.formLanguage, rb.formCountry, rb.formDescription, rb.formTags
}

// IsInputMode returns whether in input mode
func (rb *RadioBrowser) IsInputMode() bool {
	return rb.inputMode
}

// GetInputBuffer returns current input buffer
func (rb *RadioBrowser) GetInputBuffer() string {
	return rb.inputBuffer
}

// Refresh refreshes the station list
func (rb *RadioBrowser) Refresh() {
	rb.stations = rb.radioLibrary.GetStations()
	if rb.selected >= len(rb.stations) {
		rb.selected = len(rb.stations) - 1
	}
	if rb.selected < 0 {
		rb.selected = 0
	}
	rb.adjustViewport()
}

// GetQuickURL returns the quick add URL
func (rb *RadioBrowser) GetQuickURL() string {
	return rb.quickURL
}

// GetQuickName returns the quick add name
func (rb *RadioBrowser) GetQuickName() string {
	return rb.quickName
}