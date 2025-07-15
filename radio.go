package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RadioStation represents an internet radio station
type RadioStation struct {
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	StreamURL   string            `json:"stream_url"`   // Actual stream URL (resolved from PLS/M3U)
	Genre       string            `json:"genre"`
	Language    string            `json:"language"`
	Country     string            `json:"country"`
	Bitrate     string            `json:"bitrate"`
	Description string            `json:"description"`
	Tags        []string          `json:"tags"`
	Metadata    map[string]string `json:"metadata"`
	AddedAt     time.Time         `json:"added_at"`
	LastPlayed  time.Time         `json:"last_played"`
}

// RadioLibrary manages the collection of radio stations
type RadioLibrary struct {
	stations []RadioStation
	filePath string
}

// NewRadioLibrary creates a new radio library instance
func NewRadioLibrary() (*RadioLibrary, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}
	
	configDir := filepath.Join(homeDir, ".resona")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}
	
	filePath := filepath.Join(configDir, "radio_stations.json")
	
	rl := &RadioLibrary{
		stations: []RadioStation{},
		filePath: filePath,
	}
	
	// Load existing stations
	if err := rl.Load(); err != nil {
		// If file doesn't exist, that's okay - we'll create it on first save
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load radio stations: %w", err)
		}
	}
	
	return rl, nil
}

// Load reads radio stations from persistent storage
func (rl *RadioLibrary) Load() error {
	data, err := os.ReadFile(rl.filePath)
	if err != nil {
		return err
	}
	
	return json.Unmarshal(data, &rl.stations)
}

// Save writes radio stations to persistent storage
func (rl *RadioLibrary) Save() error {
	data, err := json.MarshalIndent(rl.stations, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal stations: %w", err)
	}
	
	return os.WriteFile(rl.filePath, data, 0644)
}

// AddStation adds a new radio station to the library
func (rl *RadioLibrary) AddStation(station RadioStation) error {
	station.AddedAt = time.Now()
	
	// Resolve stream URL if it's a playlist
	if strings.Contains(station.URL, ".pls") || strings.Contains(station.URL, ".m3u") {
		streamURL, err := resolvePlaylistURL(station.URL)
		if err != nil {
			return fmt.Errorf("failed to resolve playlist URL: %w", err)
		}
		station.StreamURL = streamURL
	} else {
		station.StreamURL = station.URL
	}
	
	rl.stations = append(rl.stations, station)
	return rl.Save()
}

// GetStations returns all radio stations
func (rl *RadioLibrary) GetStations() []RadioStation {
	return rl.stations
}

// GetStationByName finds a station by name
func (rl *RadioLibrary) GetStationByName(name string) (*RadioStation, bool) {
	for i, station := range rl.stations {
		if station.Name == name {
			return &rl.stations[i], true
		}
	}
	return nil, false
}

// RemoveStation removes a station by name
func (rl *RadioLibrary) RemoveStation(name string) error {
	for i, station := range rl.stations {
		if station.Name == name {
			rl.stations = append(rl.stations[:i], rl.stations[i+1:]...)
			return rl.Save()
		}
	}
	return fmt.Errorf("station not found: %s", name)
}

// GetStationsByGenre returns stations filtered by genre
func (rl *RadioLibrary) GetStationsByGenre(genre string) []RadioStation {
	var filtered []RadioStation
	for _, station := range rl.stations {
		if strings.EqualFold(station.Genre, genre) {
			filtered = append(filtered, station)
		}
	}
	return filtered
}

// GetGenres returns all unique genres
func (rl *RadioLibrary) GetGenres() []string {
	genreMap := make(map[string]bool)
	for _, station := range rl.stations {
		if station.Genre != "" {
			genreMap[station.Genre] = true
		}
	}
	
	var genres []string
	for genre := range genreMap {
		genres = append(genres, genre)
	}
	sort.Strings(genres)
	return genres
}

// resolvePlaylistURL parses PLS/M3U files and returns the first stream URL
func resolvePlaylistURL(playlistURL string) (string, error) {
	resp, err := http.Get(playlistURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch playlist: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch playlist: HTTP %d", resp.StatusCode)
	}
	
	scanner := bufio.NewScanner(resp.Body)
	
	// Detect format and parse accordingly
	if strings.Contains(playlistURL, ".pls") {
		return parsePLS(scanner)
	} else if strings.Contains(playlistURL, ".m3u") {
		return parseM3U(scanner)
	}
	
	return "", fmt.Errorf("unsupported playlist format")
}

// parsePLS parses PLS playlist format
func parsePLS(scanner *bufio.Scanner) (string, error) {
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "File1=") {
			return strings.TrimPrefix(line, "File1="), nil
		}
	}
	return "", fmt.Errorf("no stream URL found in PLS file")
}

// parseM3U parses M3U playlist format
func parseM3U(scanner *bufio.Scanner) (string, error) {
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			return line, nil
		}
	}
	return "", fmt.Errorf("no stream URL found in M3U file")
}

// UpdateLastPlayed updates the last played time for a station
func (rl *RadioLibrary) UpdateLastPlayed(name string) error {
	for i, station := range rl.stations {
		if station.Name == name {
			rl.stations[i].LastPlayed = time.Now()
			return rl.Save()
		}
	}
	return fmt.Errorf("station not found: %s", name)
}

// GetRecentStations returns stations sorted by last played time
func (rl *RadioLibrary) GetRecentStations(limit int) []RadioStation {
	// Sort by last played time (most recent first)
	sorted := make([]RadioStation, len(rl.stations))
	copy(sorted, rl.stations)
	
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].LastPlayed.After(sorted[j].LastPlayed)
	})
	
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}
	
	return sorted
}

// ClearLibrary clears all radio station data
func (rl *RadioLibrary) ClearLibrary() error {
	rl.stations = []RadioStation{}
	return rl.Save()
}