package main

import (
	"encoding/binary"
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

// supportedAudioExts are the file extensions the audio player can decode.
var supportedAudioExts = map[string]bool{
	".mp3":  true,
	".flac": true,
	".wav":  true,
	".ogg":  true,
	// .m4a and .aac are not supported by the audio player
}

func isSupportedAudio(path string) bool {
	return supportedAudioExts[strings.ToLower(filepath.Ext(path))]
}

func scanMusicLibrary(rootPath string) ([]Song, error) {
	var songs []Song
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && isSupportedAudio(path) {
			songs = append(songs, extractMetadata(path))
		}
		return nil
	})
	return songs, err
}

// countAudioFiles returns the number of supported audio files under the given
// folders. It only inspects directory entries (no file reads), so it is far
// cheaper than a full metadata scan and lets us drive a determinate progress
// bar.
func countAudioFiles(folders []string) int {
	total := 0
	for _, folder := range folders {
		filepath.Walk(folder, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && isSupportedAudio(path) {
				total++
			}
			return nil
		})
	}
	return total
}

// scanFoldersProgress scans the given folders for supported audio files and
// extracts metadata for each. onProgress, if non-nil, is invoked after each
// file with the running count and the precomputed total. It performs no shared
// mutation, so it is safe to run from a goroutine (onProgress runs on that same
// goroutine).
func scanFoldersProgress(folders []string, onProgress func(done, total int)) []Song {
	total := countAudioFiles(folders)
	if onProgress != nil {
		onProgress(0, total)
	}

	var songs []Song
	done := 0
	for _, folder := range folders {
		filepath.Walk(folder, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && isSupportedAudio(path) {
				songs = append(songs, extractMetadata(path))
				done++
				if onProgress != nil {
					onProgress(done, total)
				}
			}
			return nil
		})
	}
	return songs
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

// calculateMP3Duration returns the duration of an MP3 file. It first tries the
// fast path of reading the frame and VBR (Xing/Info/VBRI) headers; only if that
// fails does it fall back to decoding every frame (accurate but slow).
func calculateMP3Duration(filePath string) float64 {
	if d := mp3DurationFromHeader(filePath); d > 0 {
		return d
	}
	return mp3DurationByDecoding(filePath)
}

// MP3 lookup tables, indexed by the 4-bit fields of the frame header. Only
// Layer III (the "MP3" layer) is covered; other layers fall back to decoding.
var (
	mp3BitratesV1L3 = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0} // MPEG 1, kbps
	mp3BitratesV2L3 = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}     // MPEG 2/2.5, kbps
	mp3SampleRates  = map[int][3]int{
		1:  {44100, 48000, 32000}, // MPEG 1
		2:  {22050, 24000, 16000}, // MPEG 2
		25: {11025, 12000, 8000},  // MPEG 2.5
	}
)

type mp3FrameInfo struct {
	version         int // 1, 2, or 25
	bitrate         int // bits per second
	sampleRate      int // Hz
	samplesPerFrame int
	sideInfoSize    int // bytes, used to locate the Xing/Info tag
}

// parseMP3FrameHeader parses a 4-byte MPEG Layer III frame header, returning
// false for non-Layer-III, reserved, or free-format frames.
func parseMP3FrameHeader(b []byte) (mp3FrameInfo, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return mp3FrameInfo{}, false
	}
	verBits := (b[1] >> 3) & 0x3
	layerBits := (b[1] >> 1) & 0x3
	brIndex := int((b[2] >> 4) & 0xF)
	srIndex := int((b[2] >> 2) & 0x3)
	chMode := (b[3] >> 6) & 0x3

	if layerBits != 0x1 { // 0b01 == Layer III
		return mp3FrameInfo{}, false
	}
	if brIndex == 0 || brIndex == 15 || srIndex == 3 {
		return mp3FrameInfo{}, false // free-format or invalid
	}

	var fi mp3FrameInfo
	switch verBits {
	case 3: // MPEG 1
		fi.version, fi.samplesPerFrame = 1, 1152
		fi.bitrate = mp3BitratesV1L3[brIndex] * 1000
	case 2: // MPEG 2
		fi.version, fi.samplesPerFrame = 2, 576
		fi.bitrate = mp3BitratesV2L3[brIndex] * 1000
	case 0: // MPEG 2.5
		fi.version, fi.samplesPerFrame = 25, 576
		fi.bitrate = mp3BitratesV2L3[brIndex] * 1000
	default: // reserved
		return mp3FrameInfo{}, false
	}
	fi.sampleRate = mp3SampleRates[fi.version][srIndex]
	if fi.sampleRate == 0 || fi.bitrate == 0 {
		return mp3FrameInfo{}, false
	}

	mono := chMode == 0x3
	switch {
	case fi.version == 1 && mono:
		fi.sideInfoSize = 17
	case fi.version == 1:
		fi.sideInfoSize = 32
	case mono:
		fi.sideInfoSize = 9
	default:
		fi.sideInfoSize = 17
	}
	return fi, true
}

// mp3VBRFrameCount returns the total frame count from a Xing/Info or VBRI tag in
// the first frame, or 0 if neither is present (i.e. the file is likely CBR).
func mp3VBRFrameCount(frame []byte, fi mp3FrameInfo) int {
	// Xing / Info: located after the 4-byte header plus the side-info block.
	if off := 4 + fi.sideInfoSize; off+12 <= len(frame) {
		if tag := string(frame[off : off+4]); tag == "Xing" || tag == "Info" {
			flags := binary.BigEndian.Uint32(frame[off+4 : off+8])
			if flags&0x1 != 0 { // bit 0: frame count present
				return int(binary.BigEndian.Uint32(frame[off+8 : off+12]))
			}
		}
	}
	// VBRI: always 32 bytes after the header; frame count is at offset +14.
	if off := 4 + 32; off+18 <= len(frame) && string(frame[off:off+4]) == "VBRI" {
		return int(binary.BigEndian.Uint32(frame[off+14 : off+18]))
	}
	return 0
}

// mp3DurationFromHeader computes duration by reading only the first frame's
// headers: an exact value for VBR files with a Xing/VBRI tag, or a constant-
// bitrate estimate otherwise. Returns 0 if the headers can't be parsed.
func mp3DurationFromHeader(filePath string) float64 {
	f, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return 0
	}
	fileSize := stat.Size()

	// Skip an ID3v2 tag, if present, to locate the first audio frame.
	var dataStart int64
	hdr := make([]byte, 10)
	if _, err := io.ReadFull(f, hdr); err == nil && string(hdr[0:3]) == "ID3" {
		size := int64(hdr[6]&0x7F)<<21 | int64(hdr[7]&0x7F)<<14 | int64(hdr[8]&0x7F)<<7 | int64(hdr[9]&0x7F)
		dataStart = 10 + size
		if hdr[5]&0x10 != 0 {
			dataStart += 10 // ID3v2 footer present
		}
	}

	// Read a window large enough to hold the first frame and its VBR header.
	if _, err := f.Seek(dataStart, io.SeekStart); err != nil {
		return 0
	}
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]

	// Find the first valid Layer III frame header in the window.
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF {
			continue
		}
		frameInfo, ok := parseMP3FrameHeader(buf[i:])
		if !ok {
			continue
		}
		frameStart := dataStart + int64(i)

		// VBR: exact duration from the stored frame count.
		if frames := mp3VBRFrameCount(buf[i:], frameInfo); frames > 0 {
			totalSamples := int64(frames) * int64(frameInfo.samplesPerFrame)
			return float64(totalSamples) / float64(frameInfo.sampleRate)
		}

		// CBR: estimate from the audio byte length and the constant bitrate.
		audioBytes := fileSize - frameStart
		if mp3HasID3v1(f, fileSize) {
			audioBytes -= 128
		}
		if audioBytes <= 0 {
			return 0
		}
		return float64(audioBytes) * 8.0 / float64(frameInfo.bitrate)
	}
	return 0
}

func mp3HasID3v1(f *os.File, fileSize int64) bool {
	if fileSize < 128 {
		return false
	}
	tag := make([]byte, 3)
	if _, err := f.ReadAt(tag, fileSize-128); err != nil {
		return false
	}
	return string(tag) == "TAG"
}

// mp3DurationByDecoding is the slow, robust fallback: it decodes every frame.
func mp3DurationByDecoding(filePath string) float64 {
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
	return float64(totalSamples) / float64(sampleRate)
}

func calculateFLACDuration(filePath string) float64 {
	// Parse FLAC file to get the StreamInfo block, which usually contains the
	// exact sample count.
	stream, err := flac.ParseFile(filePath)
	if err != nil {
		return 0
	}
	defer stream.Close()

	info := stream.Info
	if info == nil || info.SampleRate == 0 {
		return 0
	}

	totalSamples := info.NSamples
	if totalSamples == 0 {
		// STREAMINFO is allowed to record a total sample count of 0 ("unknown"),
		// which some encoders/rippers do. The stream is positioned at the first
		// audio frame, so walk the frames and sum their block sizes rather than
		// reporting 0:00.
		for {
			frame, err := stream.ParseNext()
			if err != nil {
				break // io.EOF or a read error ends the stream
			}
			totalSamples += uint64(frame.BlockSize)
		}
	}
	if totalSamples == 0 {
		return 0
	}

	return float64(totalSamples) / float64(info.SampleRate)
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

