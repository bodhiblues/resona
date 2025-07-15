package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FolderBrowser struct {
	currentPath string
	entries     []string
	selected    int
	viewportTop int
	viewportHeight int
}

func NewFolderBrowser() (*FolderBrowser, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	
	fb := &FolderBrowser{
		currentPath:    homeDir,
		selected:       0,
		viewportTop:    0,
		viewportHeight: 20, // Default height, will be updated by main app
	}
	
	err = fb.refreshEntries()
	return fb, err
}

func (fb *FolderBrowser) refreshEntries() error {
	entries, err := os.ReadDir(fb.currentPath)
	if err != nil {
		return err
	}
	
	fb.entries = make([]string, 0, len(entries))
	
	// Add parent directory entry if not at root
	if fb.currentPath != "/" && fb.currentPath != "" {
		fb.entries = append(fb.entries, "..")
	}
	
	// Separate directories and files
	var dirs []string
	var files []string
	
	// Define supported file types
	supportedExtensions := map[string]bool{
		".mp3":  true,
		".flac": true,
		".wav":  true,
		".m4a":  true,
		".ogg":  true,
		".m3u":  true,  // Playlist files
		".m3u8": true,  // Playlist files
		".pls":  true,  // Playlist files
	}
	
	for _, entry := range entries {
		// Skip hidden files/directories
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		} else {
			// Only show music files and playlists
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if supportedExtensions[ext] {
				files = append(files, entry.Name())
			}
		}
	}
	
	// Sort directories and files separately
	sort.Strings(dirs)
	sort.Strings(files)
	
	// Add directories first, then files
	fb.entries = append(fb.entries, dirs...)
	fb.entries = append(fb.entries, files...)
	
	// Reset selection if out of bounds
	if fb.selected >= len(fb.entries) {
		fb.selected = 0
	}
	
	// Reset viewport to top
	fb.viewportTop = 0
	
	return nil
}

func (fb *FolderBrowser) GetCurrentPath() string {
	return fb.currentPath
}

func (fb *FolderBrowser) GetEntries() []string {
	return fb.entries
}

func (fb *FolderBrowser) GetSelectedIndex() int {
	return fb.selected
}

func (fb *FolderBrowser) GetSelected() string {
	if fb.selected >= 0 && fb.selected < len(fb.entries) {
		entry := fb.entries[fb.selected]
		if entry == ".." {
			return filepath.Dir(fb.currentPath)
		}
		return filepath.Join(fb.currentPath, entry)
	}
	return ""
}

func (fb *FolderBrowser) IsDirectory(path string) bool {
	if path == "" {
		return false
	}
	
	// Handle ".." case
	if filepath.Base(path) == ".." {
		return true
	}
	
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func (fb *FolderBrowser) EnterDirectory(path string) error {
	if !fb.IsDirectory(path) {
		return nil
	}
	
	fb.currentPath = path
	fb.selected = 0
	return fb.refreshEntries()
}

func (fb *FolderBrowser) GoBack() error {
	parent := filepath.Dir(fb.currentPath)
	if parent != fb.currentPath {
		fb.currentPath = parent
		fb.selected = 0
		return fb.refreshEntries()
	}
	return nil
}

func (fb *FolderBrowser) MoveUp() {
	if fb.selected > 0 {
		fb.selected--
		fb.adjustViewport()
	}
}

func (fb *FolderBrowser) MoveDown() {
	if fb.selected < len(fb.entries)-1 {
		fb.selected++
		fb.adjustViewport()
	}
}

func (fb *FolderBrowser) SetViewportHeight(height int) {
	fb.viewportHeight = height
	fb.adjustViewport()
}

func (fb *FolderBrowser) adjustViewport() {
	// Keep selected item visible
	if fb.selected < fb.viewportTop {
		fb.viewportTop = fb.selected
	} else if fb.selected >= fb.viewportTop+fb.viewportHeight {
		fb.viewportTop = fb.selected - fb.viewportHeight + 1
	}
	
	// Ensure viewport doesn't go below 0
	if fb.viewportTop < 0 {
		fb.viewportTop = 0
	}
}

func (fb *FolderBrowser) GetVisibleEntries() []string {
	if len(fb.entries) == 0 {
		return []string{}
	}
	
	end := fb.viewportTop + fb.viewportHeight
	if end > len(fb.entries) {
		end = len(fb.entries)
	}
	
	return fb.entries[fb.viewportTop:end]
}

func (fb *FolderBrowser) GetVisibleSelectedIndex() int {
	return fb.selected - fb.viewportTop
}