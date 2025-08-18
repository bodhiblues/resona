package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type LibraryBrowser struct {
	currentPane      string // "categories" or "contents"
	categoryType     string // "artists", "albums", "genres"
	categories       []string
	contents         []LibraryItem
	categoryIndex    int
	contentIndex     int
	breadcrumb       []string // Track navigation path
	libraryManager   *LibraryManager
	categoryViewport viewport
	contentViewport  viewport
}

type viewport struct {
	top    int
	height int
}

type LibraryItem struct {
	Type     string // "artist", "album", "song"
	Title    string
	Subtitle string // For albums: artist, for songs: artist - album
	Song     *Song  // Only for song items
}

func NewLibraryBrowser(libraryManager *LibraryManager) *LibraryBrowser {
	lb := &LibraryBrowser{
		currentPane:      "contents", // Start in contents pane
		categoryType:     "artists",
		libraryManager:   libraryManager,
		categoryIndex:    0,
		contentIndex:     0,
		breadcrumb:       []string{},
		categoryViewport: viewport{top: 0, height: 20}, // Default height
		contentViewport:  viewport{top: 0, height: 20}, // Default height
	}
	
	lb.refreshCategories()
	lb.refreshContents()
	
	return lb
}

func (lb *LibraryBrowser) refreshCategories() {
	lb.categories = []string{"Artists", "Albums", "Genres"}
	
	// Set categoryIndex based on current categoryType
	switch lb.categoryType {
	case "artists":
		lb.categoryIndex = 0
	case "albums":
		lb.categoryIndex = 1
	case "genres":
		lb.categoryIndex = 2
	}
}

func (lb *LibraryBrowser) refreshContents() {
	songs := lb.libraryManager.GetSongs()
	if len(songs) == 0 {
		lb.contents = []LibraryItem{
			{Type: "empty", Title: "No songs in library", Subtitle: ""},
		}
		return
	}
	
	switch lb.categoryType {
	case "artists":
		lb.contents = lb.getArtists(songs)
	case "albums":
		lb.contents = lb.getAlbums(songs)
	case "genres":
		lb.contents = lb.getGenres(songs)
	}
	
	if lb.contentIndex >= len(lb.contents) {
		lb.contentIndex = 0
	}
}

func (lb *LibraryBrowser) getArtists(songs []Song) []LibraryItem {
	artistMap := make(map[string][]Song)
	
	for _, song := range songs {
		artist := song.Artist
		if artist == "" {
			artist = "Unknown Artist"
		}
		artistMap[artist] = append(artistMap[artist], song)
	}
	
	var artists []string
	for artist := range artistMap {
		artists = append(artists, artist)
	}
	sort.Strings(artists)
	
	var items []LibraryItem
	for _, artist := range artists {
		albumCount := len(lb.getUniqueAlbums(artistMap[artist]))
		songCount := len(artistMap[artist])
		subtitle := ""
		if albumCount == 1 {
			subtitle = "1 album"
		} else {
			subtitle = strconv.Itoa(albumCount) + " albums"
		}
		subtitle += ", " + strconv.Itoa(songCount) + " songs"
		
		items = append(items, LibraryItem{
			Type:     "artist",
			Title:    artist,
			Subtitle: subtitle,
		})
	}
	
	return items
}

func (lb *LibraryBrowser) getAlbums(songs []Song) []LibraryItem {
	albumMap := make(map[string][]Song)
	
	for _, song := range songs {
		album := song.Album
		if album == "" {
			album = "Unknown Album"
		}
		key := album + " - " + song.Artist
		albumMap[key] = append(albumMap[key], song)
	}
	
	var albums []string
	for album := range albumMap {
		albums = append(albums, album)
	}
	sort.Strings(albums)
	
	var items []LibraryItem
	for _, albumKey := range albums {
		songs := albumMap[albumKey]
		parts := strings.Split(albumKey, " - ")
		album := parts[0]
		artist := "Unknown Artist"
		if len(parts) > 1 {
			artist = parts[1]
		}
		
		items = append(items, LibraryItem{
			Type:     "album",
			Title:    album,
			Subtitle: artist + " • " + strconv.Itoa(len(songs)) + " songs",
		})
	}
	
	return items
}

func (lb *LibraryBrowser) getGenres(songs []Song) []LibraryItem {
	genreMap := make(map[string][]Song)
	
	for _, song := range songs {
		genre := song.Genre
		if genre == "" {
			genre = "Unknown Genre"
		}
		genreMap[genre] = append(genreMap[genre], song)
	}
	
	var genres []string
	for genre := range genreMap {
		genres = append(genres, genre)
	}
	sort.Strings(genres)
	
	var items []LibraryItem
	for _, genre := range genres {
		songs := genreMap[genre]
		artistCount := len(lb.getUniqueArtists(songs))
		
		subtitle := ""
		if artistCount == 1 {
			subtitle = "1 artist"
		} else {
			subtitle = strconv.Itoa(artistCount) + " artists"
		}
		subtitle += ", " + strconv.Itoa(len(songs)) + " songs"
		
		items = append(items, LibraryItem{
			Type:     "genre",
			Title:    genre,
			Subtitle: subtitle,
		})
	}
	
	return items
}

func (lb *LibraryBrowser) getUniqueAlbums(songs []Song) map[string]bool {
	albums := make(map[string]bool)
	for _, song := range songs {
		album := song.Album
		if album == "" {
			album = "Unknown Album"
		}
		albums[album] = true
	}
	return albums
}

func (lb *LibraryBrowser) getUniqueArtists(songs []Song) map[string]bool {
	artists := make(map[string]bool)
	for _, song := range songs {
		artist := song.Artist
		if artist == "" {
			artist = "Unknown Artist"
		}
		artists[artist] = true
	}
	return artists
}

func (lb *LibraryBrowser) SwitchPane() {
	if lb.currentPane == "categories" {
		lb.currentPane = "contents"
	} else {
		lb.currentPane = "categories"
	}
}

func (lb *LibraryBrowser) MoveUp() {
	if lb.currentPane == "categories" {
		if lb.categoryIndex > 0 {
			lb.categoryIndex--
			lb.adjustCategoryViewport()
			lb.updateCategoryType()
		}
	} else {
		if lb.contentIndex > 0 {
			lb.contentIndex--
			lb.adjustContentViewport()
		}
	}
}

func (lb *LibraryBrowser) MoveDown() {
	if lb.currentPane == "categories" {
		if lb.categoryIndex < len(lb.categories)-1 {
			lb.categoryIndex++
			lb.adjustCategoryViewport()
			lb.updateCategoryType()
		}
	} else {
		if lb.contentIndex < len(lb.contents)-1 {
			lb.contentIndex++
			lb.adjustContentViewport()
		}
	}
}

func (lb *LibraryBrowser) SetViewportHeight(height int) {
	// Reserve space for header and status
	viewportHeight := height - 6
	if viewportHeight < 5 {
		viewportHeight = 5
	}
	
	lb.categoryViewport.height = viewportHeight
	lb.contentViewport.height = viewportHeight
	
	lb.adjustCategoryViewport()
	lb.adjustContentViewport()
}

func (lb *LibraryBrowser) adjustCategoryViewport() {
	// Keep selected category visible
	if lb.categoryIndex < lb.categoryViewport.top {
		lb.categoryViewport.top = lb.categoryIndex
	} else if lb.categoryIndex >= lb.categoryViewport.top+lb.categoryViewport.height {
		lb.categoryViewport.top = lb.categoryIndex - lb.categoryViewport.height + 1
	}
	
	if lb.categoryViewport.top < 0 {
		lb.categoryViewport.top = 0
	}
}

func (lb *LibraryBrowser) adjustContentViewport() {
	// Only adjust if the selected content is actually outside the viewport
	// This prevents unnecessary viewport shifts when height changes slightly
	if lb.contentIndex < lb.contentViewport.top {
		lb.contentViewport.top = lb.contentIndex
	} else if lb.contentIndex >= lb.contentViewport.top+lb.contentViewport.height {
		// Only adjust if we really need to (selected item is not visible)
		if lb.contentViewport.height > 0 {
			lb.contentViewport.top = lb.contentIndex - lb.contentViewport.height + 1
		}
	}
	
	// Ensure we don't go below 0
	if lb.contentViewport.top < 0 {
		lb.contentViewport.top = 0
	}
	
	// Ensure we don't exceed content bounds
	if len(lb.contents) > 0 && lb.contentViewport.top >= len(lb.contents) {
		lb.contentViewport.top = len(lb.contents) - 1
		if lb.contentViewport.top < 0 {
			lb.contentViewport.top = 0
		}
	}
}

func (lb *LibraryBrowser) updateCategoryType() {
	switch lb.categoryIndex {
	case 0:
		lb.categoryType = "artists"
	case 1:
		lb.categoryType = "albums"
	case 2:
		lb.categoryType = "genres"
	}
	lb.contentIndex = 0
	lb.breadcrumb = []string{}
	lb.refreshContents()
}

func (lb *LibraryBrowser) EnterSelected() *Song {
	if lb.currentPane != "contents" || len(lb.contents) == 0 {
		return nil
	}
	
	selected := lb.contents[lb.contentIndex]
	
	switch selected.Type {
	case "artist":
		return lb.drillDownToArtist(selected.Title)
	case "album":
		return lb.drillDownToAlbum(selected.Title, selected.Subtitle)
	case "genre":
		return lb.drillDownToGenre(selected.Title)
	case "song":
		return selected.Song
	}
	
	return nil
}

func (lb *LibraryBrowser) drillDownToArtist(artist string) *Song {
	songs := lb.libraryManager.GetSongs()
	var artistSongs []Song
	
	for _, song := range songs {
		if song.Artist == artist || (song.Artist == "" && artist == "Unknown Artist") {
			artistSongs = append(artistSongs, song)
		}
	}
	
	// Show albums for this artist
	albums := lb.getUniqueAlbums(artistSongs)
	var items []LibraryItem
	
	for album := range albums {
		var albumSongs []Song
		for _, song := range artistSongs {
			songAlbum := song.Album
			if songAlbum == "" {
				songAlbum = "Unknown Album"
			}
			if songAlbum == album {
				albumSongs = append(albumSongs, song)
			}
		}
		
		items = append(items, LibraryItem{
			Type:     "album",
			Title:    album,
			Subtitle: artist + " • " + strconv.Itoa(len(albumSongs)) + " songs",
		})
	}
	
	// Sort albums
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	
	lb.contents = items
	lb.contentIndex = 0
	lb.breadcrumb = []string{artist}
	
	return nil
}

func (lb *LibraryBrowser) drillDownToAlbum(album, subtitle string) *Song {
	// Extract artist from subtitle
	parts := strings.Split(subtitle, " • ")
	artist := parts[0]
	
	songs := lb.libraryManager.GetSongs()
	var albumSongs []Song
	
	for _, song := range songs {
		songArtist := song.Artist
		if songArtist == "" {
			songArtist = "Unknown Artist"
		}
		songAlbum := song.Album
		if songAlbum == "" {
			songAlbum = "Unknown Album"
		}
		
		if songArtist == artist && songAlbum == album {
			albumSongs = append(albumSongs, song)
		}
	}
	
	// Sort by track number if available, otherwise by title
	sort.Slice(albumSongs, func(i, j int) bool {
		// If both songs have track numbers, sort by track number
		if albumSongs[i].TrackNumber > 0 && albumSongs[j].TrackNumber > 0 {
			return albumSongs[i].TrackNumber < albumSongs[j].TrackNumber
		}
		// If only one has a track number, prioritize it
		if albumSongs[i].TrackNumber > 0 && albumSongs[j].TrackNumber == 0 {
			return true
		}
		if albumSongs[i].TrackNumber == 0 && albumSongs[j].TrackNumber > 0 {
			return false
		}
		// If neither has track numbers, sort alphabetically by title
		return strings.ToLower(albumSongs[i].Title) < strings.ToLower(albumSongs[j].Title)
	})
	
	// Show songs in this album
	var items []LibraryItem
	for _, song := range albumSongs {
		// Format title with track number if available
		title := song.Title
		if song.TrackNumber > 0 {
			title = fmt.Sprintf("%d. %s", song.TrackNumber, song.Title)
		}
		
		items = append(items, LibraryItem{
			Type:     "song",
			Title:    title,
			Subtitle: song.Artist + " - " + song.Album,
			Song:     &song,
		})
	}
	
	lb.contents = items
	lb.contentIndex = 0
	if len(lb.breadcrumb) == 0 {
		lb.breadcrumb = []string{artist, album}
	} else {
		lb.breadcrumb = append(lb.breadcrumb, album)
	}
	
	return nil
}

func (lb *LibraryBrowser) drillDownToGenre(genre string) *Song {
	songs := lb.libraryManager.GetSongs()
	var genreSongs []Song
	
	for _, song := range songs {
		songGenre := song.Genre
		if songGenre == "" {
			songGenre = "Unknown Genre"
		}
		if songGenre == genre {
			genreSongs = append(genreSongs, song)
		}
	}
	
	// Show artists in this genre
	artists := lb.getUniqueArtists(genreSongs)
	var items []LibraryItem
	
	for artist := range artists {
		var artistSongs []Song
		for _, song := range genreSongs {
			songArtist := song.Artist
			if songArtist == "" {
				songArtist = "Unknown Artist"
			}
			if songArtist == artist {
				artistSongs = append(artistSongs, song)
			}
		}
		
		albums := lb.getUniqueAlbums(artistSongs)
		subtitle := ""
		if len(albums) == 1 {
			subtitle = "1 album"
		} else {
			subtitle = strconv.Itoa(len(albums)) + " albums"
		}
		subtitle += ", " + strconv.Itoa(len(artistSongs)) + " songs"
		
		items = append(items, LibraryItem{
			Type:     "artist",
			Title:    artist,
			Subtitle: subtitle,
		})
	}
	
	// Sort artists
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	
	lb.contents = items
	lb.contentIndex = 0
	lb.breadcrumb = []string{genre}
	
	return nil
}

func (lb *LibraryBrowser) GoBack() {
	if len(lb.breadcrumb) == 0 {
		return
	}
	
	// Remove last breadcrumb
	lb.breadcrumb = lb.breadcrumb[:len(lb.breadcrumb)-1]
	
	if len(lb.breadcrumb) == 0 {
		// Back to top level
		lb.refreshContents()
	} else {
		// Navigate back up the hierarchy
		// This would need more complex logic for different paths
		lb.refreshContents()
	}
	
	lb.contentIndex = 0
	// Reset viewport to ensure proper scrolling
	lb.contentViewport.top = 0
	lb.adjustContentViewport()
}

func (lb *LibraryBrowser) GetCategories() []string {
	return lb.categories
}

func (lb *LibraryBrowser) GetContents() []LibraryItem {
	return lb.contents
}

func (lb *LibraryBrowser) GetCategoryIndex() int {
	return lb.categoryIndex
}

func (lb *LibraryBrowser) GetContentIndex() int {
	return lb.contentIndex
}

func (lb *LibraryBrowser) GetCurrentPane() string {
	return lb.currentPane
}

func (lb *LibraryBrowser) GetBreadcrumb() []string {
	return lb.breadcrumb
}

func (lb *LibraryBrowser) Refresh() {
	lb.refreshCategories()
	lb.refreshContents()
	lb.ResetViewportOnly()
}

func (lb *LibraryBrowser) GetCategoryType() string {
	return lb.categoryType
}

func (lb *LibraryBrowser) ResetViewport() {
	lb.contentViewport.top = 0
	lb.categoryViewport.top = 0
	lb.contentIndex = 0
	lb.categoryIndex = 0
	lb.adjustContentViewport()
	lb.adjustCategoryViewport()
}

func (lb *LibraryBrowser) ForceViewportReset() {
	// Force a complete viewport reset with bounds checking
	lb.contentViewport.top = 0
	lb.categoryViewport.top = 0
	lb.contentIndex = 0
	lb.categoryIndex = 0
	
	// Ensure indices are within bounds
	if len(lb.contents) > 0 && lb.contentIndex >= len(lb.contents) {
		lb.contentIndex = 0
	}
	if len(lb.categories) > 0 && lb.categoryIndex >= len(lb.categories) {
		lb.categoryIndex = 0
	}
	
	// Force viewport adjustment
	lb.adjustContentViewport()
	lb.adjustCategoryViewport()
}

func (lb *LibraryBrowser) ResetViewportOnly() {
	lb.contentViewport.top = 0
	lb.categoryViewport.top = 0
	lb.adjustContentViewport()
	lb.adjustCategoryViewport()
}