package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Playlist is a named, ordered collection of songs. Full Song structs are stored
// so a playlist is self-contained (no library lookup needed to play it).
type Playlist struct {
	Name  string `json:"name"`
	Songs []Song `json:"songs"`
}

// PlaylistData is the on-disk shape of playlists.json.
type PlaylistData struct {
	Playlists []Playlist `json:"playlists"`
}

// PlaylistManager owns the user's playlists and persists them to
// ~/.resona/playlists.json. It mirrors LibraryManager's persistence pattern.
type PlaylistManager struct {
	playlists    []Playlist
	playlistFile string
}

func NewPlaylistManager() (*PlaylistManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	configDir := filepath.Join(homeDir, ".resona")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %v", err)
	}

	pm := &PlaylistManager{
		playlists:    []Playlist{},
		playlistFile: filepath.Join(configDir, "playlists.json"),
	}

	// A missing or unreadable file just means "no playlists yet".
	_ = pm.Load()
	return pm, nil
}

// Load reads playlists from disk. A missing file is not an error (first run).
func (pm *PlaylistManager) Load() error {
	data, err := os.ReadFile(pm.playlistFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var pd PlaylistData
	if err := json.Unmarshal(data, &pd); err != nil {
		return err
	}
	pm.playlists = pd.Playlists
	return nil
}

// Save writes all playlists to disk.
func (pm *PlaylistManager) Save() error {
	data, err := json.MarshalIndent(PlaylistData{Playlists: pm.playlists}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pm.playlistFile, data, 0644)
}

// GetPlaylists returns all playlists.
func (pm *PlaylistManager) GetPlaylists() []Playlist {
	return pm.playlists
}

// Names returns the playlist names in order.
func (pm *PlaylistManager) Names() []string {
	names := make([]string, len(pm.playlists))
	for i, p := range pm.playlists {
		names[i] = p.Name
	}
	return names
}

// index returns the position of a playlist by name, or -1.
func (pm *PlaylistManager) index(name string) int {
	for i, p := range pm.playlists {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Get returns a pointer to the named playlist, or nil.
func (pm *PlaylistManager) Get(name string) *Playlist {
	if i := pm.index(name); i >= 0 {
		return &pm.playlists[i]
	}
	return nil
}

// Create adds a new empty playlist. It errors on a blank or duplicate name.
func (pm *PlaylistManager) Create(name string) error {
	if name == "" {
		return fmt.Errorf("playlist name cannot be empty")
	}
	if pm.index(name) >= 0 {
		return fmt.Errorf("playlist %q already exists", name)
	}
	pm.playlists = append(pm.playlists, Playlist{Name: name})
	return pm.Save()
}

// Delete removes the named playlist.
func (pm *PlaylistManager) Delete(name string) error {
	i := pm.index(name)
	if i < 0 {
		return fmt.Errorf("playlist %q not found", name)
	}
	pm.playlists = append(pm.playlists[:i], pm.playlists[i+1:]...)
	return pm.Save()
}

// Rename changes a playlist's name, erroring if the new name is blank/taken.
func (pm *PlaylistManager) Rename(oldName, newName string) error {
	if newName == "" {
		return fmt.Errorf("playlist name cannot be empty")
	}
	i := pm.index(oldName)
	if i < 0 {
		return fmt.Errorf("playlist %q not found", oldName)
	}
	if newName != oldName && pm.index(newName) >= 0 {
		return fmt.Errorf("playlist %q already exists", newName)
	}
	pm.playlists[i].Name = newName
	return pm.Save()
}

// AddSongs appends songs to a playlist, creating it if needed and skipping any
// whose FilePath is already present. Returns how many were actually added.
func (pm *PlaylistManager) AddSongs(name string, songs []Song) (int, error) {
	if name == "" {
		return 0, fmt.Errorf("playlist name cannot be empty")
	}
	i := pm.index(name)
	if i < 0 {
		pm.playlists = append(pm.playlists, Playlist{Name: name})
		i = len(pm.playlists) - 1
	}

	existing := make(map[string]bool)
	for _, s := range pm.playlists[i].Songs {
		existing[s.FilePath] = true
	}

	added := 0
	for _, s := range songs {
		if s.FilePath == "" || existing[s.FilePath] {
			continue
		}
		existing[s.FilePath] = true
		pm.playlists[i].Songs = append(pm.playlists[i].Songs, s)
		added++
	}

	if added == 0 {
		return 0, nil
	}
	return added, pm.Save()
}

// RemoveSong removes the song with the given FilePath from a playlist.
func (pm *PlaylistManager) RemoveSong(name, filePath string) error {
	i := pm.index(name)
	if i < 0 {
		return fmt.Errorf("playlist %q not found", name)
	}
	songs := pm.playlists[i].Songs
	for j, s := range songs {
		if s.FilePath == filePath {
			pm.playlists[i].Songs = append(songs[:j], songs[j+1:]...)
			return pm.Save()
		}
	}
	return nil
}

// SongsOf returns a copy of a playlist's songs (nil if it doesn't exist).
func (pm *PlaylistManager) SongsOf(name string) []Song {
	if p := pm.Get(name); p != nil {
		out := make([]Song, len(p.Songs))
		copy(out, p.Songs)
		return out
	}
	return nil
}

// playlistCountLabel renders "1 song" / "N songs".
func playlistCountLabel(n int) string {
	if n == 1 {
		return "1 song"
	}
	return strconv.Itoa(n) + " songs"
}
