package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abema/go-mp4"
	"github.com/dhowden/tag"
	"github.com/go-audio/wav"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
	"github.com/tcolgate/mp3"
)

type Song struct {
	Title         string
	Artist        string
	Album         string
	Genre         string
	TrackNumber   int     // Track number within the album
	FilePath      string
	Duration      string  // Human readable duration (e.g., "3:45")
	DurationSecs  float64 // Duration in seconds for calculations
}

func scanMusicLibrary(rootPath string) ([]Song, error) {
	var songs []Song
	supportedFormats := map[string]bool{
		".mp3":  true,
		".flac": true,
		".wav":  true,
		".m4a":  true,
		".ogg":  true,
	}

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if supportedFormats[ext] {
				song := extractMetadata(path)
				songs = append(songs, song)
			}
		}
		return nil
	})

	return songs, err
}

func getFilenameWithoutExt(filePath string) string {
	base := filepath.Base(filePath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func extractMetadata(filePath string) Song {
	// Default song with filename as title
	song := Song{
		Title:        getFilenameWithoutExt(filePath),
		Artist:       "Unknown Artist",
		Album:        "Unknown Album",
		Genre:        "Unknown Genre",
		TrackNumber:  0,
		FilePath:     filePath,
		Duration:     "0:00",
		DurationSecs: 0,
	}

	// Try to read metadata from file
	file, err := os.Open(filePath)
	if err != nil {
		return song
	}
	defer file.Close()

	metadata, err := tag.ReadFrom(file)
	if err == nil {
		// Update song with metadata if available
		if title := metadata.Title(); title != "" {
			song.Title = title
		}
		if artist := metadata.Artist(); artist != "" {
			song.Artist = artist
		}
		if album := metadata.Album(); album != "" {
			song.Album = album
		}
		if genre := metadata.Genre(); genre != "" {
			song.Genre = genre
		}
		if track, total := metadata.Track(); track > 0 {
			song.TrackNumber = track
			_ = total // We have the total tracks if needed later
		}
	}
	
	// If no track number found in metadata, try to extract from filename
	if song.TrackNumber == 0 {
		if trackNum := extractTrackFromFilename(filepath.Base(filePath)); trackNum > 0 {
			song.TrackNumber = trackNum
		}
	}

	// Calculate duration based on file type
	duration := calculateDuration(filePath)
	if duration > 0 {
		song.DurationSecs = duration
		song.Duration = formatDuration(time.Duration(duration * float64(time.Second)))
	}

	return song
}

func calculateDuration(filePath string) float64 {
	ext := strings.ToLower(filepath.Ext(filePath))
	
	switch ext {
	case ".mp3":
		return calculateMP3Duration(filePath)
	case ".flac":
		return calculateFLACDuration(filePath)
	case ".wav":
		return calculateWAVDuration(filePath)
	case ".m4a":
		return calculateM4ADuration(filePath)
	case ".ogg":
		return calculateOGGDuration(filePath)
	default:
		return 0
	}
}

func calculateMP3Duration(filePath string) float64 {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	decoder := mp3.NewDecoder(file)
	var totalFrames int
	var sampleRate int
	
	for {
		frame := mp3.Frame{}
		err := decoder.Decode(&frame, &sampleRate)
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0
		}
		totalFrames++
	}
	
	if sampleRate == 0 || totalFrames == 0 {
		return 0
	}
	
	// Each MP3 frame contains 1152 samples for MPEG-1 Layer III
	samplesPerFrame := 1152
	totalSamples := totalFrames * samplesPerFrame
	duration := float64(totalSamples) / float64(sampleRate)
	
	return duration
}

func calculateFLACDuration(filePath string) float64 {
	// Parse FLAC file to get StreamInfo block which contains exact duration
	stream, err := flac.ParseFile(filePath)
	if err != nil {
		return 0
	}

	// Get the StreamInfo metadata block
	info := stream.Info
	if info == nil {
		return 0
	}

	// Check if we have valid sample rate
	if info.SampleRate == 0 {
		return 0
	}

	// Get total samples from StreamInfo block
	totalSamples := info.NSamples
	if totalSamples == 0 {
		return 0
	}

	// Calculate exact duration: total_samples / sample_rate
	duration := float64(totalSamples) / float64(info.SampleRate)
	return duration
}

func calculateWAVDuration(filePath string) float64 {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	decoder := wav.NewDecoder(file)
	if !decoder.IsValidFile() {
		return 0
	}

	format := decoder.Format()
	if format.SampleRate == 0 {
		return 0
	}

	// Get the total number of samples
	totalSamples := decoder.PCMLen()
	duration := float64(totalSamples) / float64(format.SampleRate)

	return duration
}

func calculateM4ADuration(filePath string) float64 {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	_, err = mp4.ReadBoxStructure(file, func(h *mp4.ReadHandle) (interface{}, error) {
		// This is a simplified approach - in a real implementation
		// we would need to parse the mvhd box to get duration
		return nil, nil
	})

	if err != nil {
		return 0
	}

	// For now, return 0 as M4A duration calculation is complex
	// and would require more detailed MP4 container parsing
	return 0
}

func calculateOGGDuration(filePath string) float64 {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	reader, err := oggvorbis.NewReader(file)
	if err != nil {
		return 0
	}

	// Get the total length in samples
	totalSamples := reader.Length()
	sampleRate := reader.SampleRate()

	if sampleRate == 0 {
		return 0
	}

	duration := float64(totalSamples) / float64(sampleRate)
	return duration
}

func formatDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// extractTrackFromFilename attempts to extract track number from filename
// Handles formats like: "01 - Song.flac", "1. Song.flac", "01_Song.flac", "Track 01 - Song.flac"
func extractTrackFromFilename(filename string) int {
	// Remove file extension
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	
	// Try different regex patterns to extract track numbers
	patterns := []string{
		`^(\d{1,2})\s*[-._]\s*`, // "01 - Song", "1. Song", "01_Song"
		`^Track\s+(\d{1,2})\s*[-._]\s*`, // "Track 01 - Song"
		`^(\d{1,2})\s+`, // "01 Song"
		`\b(\d{1,2})\b`, // Any 1-2 digit number
	}
	
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		
		matches := re.FindStringSubmatch(baseName)
		if len(matches) >= 2 {
			if trackNum, err := strconv.Atoi(matches[1]); err == nil && trackNum > 0 && trackNum <= 99 {
				return trackNum
			}
		}
	}
	
	return 0
}

