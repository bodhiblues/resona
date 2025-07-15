package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type LibraryManager struct {
	libraryFile string
	folders     []string
	songs       []Song
}

type LibraryData struct {
	Folders []string `json:"folders"`
	Songs   []Song   `json:"songs"`
}

func NewLibraryManager() (*LibraryManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	
	// Create .resona directory if it doesn't exist
	configDir := filepath.Join(homeDir, ".resona")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %v", err)
	}
	
	libraryFile := filepath.Join(configDir, "library.json")
	
	lm := &LibraryManager{
		libraryFile: libraryFile,
		folders:     []string{},
		songs:       []Song{},
	}
	
	// Load existing library if it exists
	if err := lm.LoadLibrary(); err != nil {
		// If loading fails, start with empty library
		lm.songs = []Song{
			{Title: "No library loaded - Press 'f' to browse folders, 'a' to add folder to library", FilePath: ""},
		}
	}
	
	return lm, nil
}

func (lm *LibraryManager) LoadLibrary() error {
	data, err := os.ReadFile(lm.libraryFile)
	if err != nil {
		return err
	}
	
	var libraryData LibraryData
	if err := json.Unmarshal(data, &libraryData); err != nil {
		return err
	}
	
	lm.folders = libraryData.Folders
	lm.songs = libraryData.Songs
	
	// If no songs but we have folders, rescan
	if len(lm.songs) == 0 && len(lm.folders) > 0 {
		return lm.RescanLibrary()
	}
	
	return nil
}

func (lm *LibraryManager) SaveLibrary() error {
	libraryData := LibraryData{
		Folders: lm.folders,
		Songs:   lm.songs,
	}
	
	data, err := json.MarshalIndent(libraryData, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(lm.libraryFile, data, 0644)
}

func (lm *LibraryManager) AddFolder(folderPath string) error {
	// Check if folder already exists
	for _, folder := range lm.folders {
		if folder == folderPath {
			return nil // Already exists
		}
	}
	
	// Add folder to list
	lm.folders = append(lm.folders, folderPath)
	sort.Strings(lm.folders)
	
	// Scan the folder for songs
	songs, err := scanMusicLibrary(folderPath)
	if err != nil {
		return err
	}
	
	// Add new songs to library, avoiding duplicates
	for _, newSong := range songs {
		isDuplicate := false
		for _, existingSong := range lm.songs {
			if existingSong.FilePath == newSong.FilePath {
				isDuplicate = true
				break
			}
		}
		if !isDuplicate {
			lm.songs = append(lm.songs, newSong)
		}
	}
	
	// Sort songs by title
	sort.Slice(lm.songs, func(i, j int) bool {
		return strings.ToLower(lm.songs[i].Title) < strings.ToLower(lm.songs[j].Title)
	})
	
	// Save library
	return lm.SaveLibrary()
}

func (lm *LibraryManager) RemoveFolder(folderPath string) error {
	// Remove folder from list
	newFolders := []string{}
	for _, folder := range lm.folders {
		if folder != folderPath {
			newFolders = append(newFolders, folder)
		}
	}
	lm.folders = newFolders
	
	// Remove songs from the folder
	newSongs := []Song{}
	for _, song := range lm.songs {
		if !strings.HasPrefix(song.FilePath, folderPath) {
			newSongs = append(newSongs, song)
		}
	}
	lm.songs = newSongs
	
	// Save library
	return lm.SaveLibrary()
}

func (lm *LibraryManager) RescanLibrary() error {
	lm.songs = []Song{}
	
	// Scan all folders
	for _, folder := range lm.folders {
		songs, err := scanMusicLibrary(folder)
		if err != nil {
			continue // Skip folders that can't be scanned
		}
		
		// Add songs, avoiding duplicates
		for _, newSong := range songs {
			isDuplicate := false
			for _, existingSong := range lm.songs {
				if existingSong.FilePath == newSong.FilePath {
					isDuplicate = true
					break
				}
			}
			if !isDuplicate {
				lm.songs = append(lm.songs, newSong)
			}
		}
	}
	
	// Sort songs by title
	sort.Slice(lm.songs, func(i, j int) bool {
		return strings.ToLower(lm.songs[i].Title) < strings.ToLower(lm.songs[j].Title)
	})
	
	// Save library
	return lm.SaveLibrary()
}

func (lm *LibraryManager) GetSongs() []Song {
	if len(lm.songs) == 0 {
		return []Song{
			{Title: "No songs in library - Press 'f' to browse folders, 'a' to add folder to library", FilePath: ""},
		}
	}
	return lm.songs
}

func (lm *LibraryManager) GetFolders() []string {
	return lm.folders
}

func (lm *LibraryManager) GetSongCount() int {
	return len(lm.songs)
}

func (lm *LibraryManager) GetFolderCount() int {
	return len(lm.folders)
}

// ClearLibrary clears all music library data
func (lm *LibraryManager) ClearLibrary() error {
	lm.folders = []string{}
	lm.songs = []Song{
		{Title: "No library loaded - Press 'f' to browse folders, 'a' to add folder to library", FilePath: ""},
	}
	return lm.SaveLibrary()
}