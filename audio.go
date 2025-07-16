package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep"
	"github.com/gopxl/beep/flac"
	"github.com/gopxl/beep/mp3"
	"github.com/gopxl/beep/speaker"
	"github.com/gopxl/beep/vorbis"
	"github.com/gopxl/beep/wav"
)

// bufferedHTTPReader wraps a buffered reader with proper close handling
type bufferedHTTPReader struct {
	reader *bufio.Reader
	closer io.Closer
}

func (b *bufferedHTTPReader) Read(p []byte) (n int, err error) {
	n, err = b.reader.Read(p)
	if err != nil && err != io.EOF {
		log.Printf("DEBUG: bufferedHTTPReader read error: %v", err)
	}
	return n, err
}

func (b *bufferedHTTPReader) Close() error {
	log.Printf("DEBUG: Closing bufferedHTTPReader")
	return b.closer.Close()
}

// SampleCaptureStreamer wraps another streamer and captures audio samples for visualization
type SampleCaptureStreamer struct {
	streamer     beep.Streamer
	audioPlayer  *AudioPlayer
	sampleBuffer []float64
	bufferSize   int
}

func NewSampleCaptureStreamer(streamer beep.Streamer, audioPlayer *AudioPlayer) *SampleCaptureStreamer {
	return &SampleCaptureStreamer{
		streamer:     streamer,
		audioPlayer:  audioPlayer,
		sampleBuffer: make([]float64, 0, 1024), // Buffer for collecting samples
		bufferSize:   1024,
	}
}

func (s *SampleCaptureStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	// Stream from the underlying streamer
	n, ok = s.streamer.Stream(samples)
	
	// Capture samples for visualization
	if ok && n > 0 {
		for i := 0; i < n; i++ {
			// Mix left and right channels and add to buffer
			mixed := (samples[i][0] + samples[i][1]) / 2.0
			s.sampleBuffer = append(s.sampleBuffer, mixed)
		}
		
		// When buffer is full, analyze and store
		if len(s.sampleBuffer) >= s.bufferSize {
			s.analyzeAndStore()
			s.sampleBuffer = s.sampleBuffer[:0] // Clear buffer
		}
	}
	
	return n, ok
}

func (s *SampleCaptureStreamer) Err() error {
	return s.streamer.Err()
}

// analyzeAndStore processes the audio samples and stores amplitude data
func (s *SampleCaptureStreamer) analyzeAndStore() {
	if s.audioPlayer == nil {
		return
	}
	
	// Calculate amplitude levels for different frequency bands
	// For simplicity, we'll create bands by analyzing different segments of the sample buffer
	numBands := 24 // Match visualizer width
	bandSize := len(s.sampleBuffer) / numBands
	
	if bandSize == 0 {
		return
	}
	
	amplitudes := make([]float64, numBands)
	
	for band := 0; band < numBands; band++ {
		startIdx := band * bandSize
		endIdx := startIdx + bandSize
		if endIdx > len(s.sampleBuffer) {
			endIdx = len(s.sampleBuffer)
		}
		
		// Calculate RMS (Root Mean Square) for this band
		var sum float64
		for i := startIdx; i < endIdx; i++ {
			sum += s.sampleBuffer[i] * s.sampleBuffer[i]
		}
		
		rms := math.Sqrt(sum / float64(endIdx-startIdx))
		amplitudes[band] = rms
	}
	
	// Store the amplitudes in the audio player
	s.audioPlayer.sampleMutex.Lock()
	s.audioPlayer.audioSamples = amplitudes
	s.audioPlayer.sampleMutex.Unlock()
}

type AudioPlayer struct {
	ctrl        *beep.Ctrl
	mixer       *beep.Mixer
	isPlaying   bool
	isPaused    bool
	currentSong string
	mutex       sync.RWMutex
	speakerInit bool
	startTime   time.Time
	pausedTime  time.Duration
	duration    float64 // Total duration in seconds
	// Audio visualization
	audioSamples []float64
	sampleMutex  sync.RWMutex
}

func NewAudioPlayer() (*AudioPlayer, error) {
	// Setup debug logging to a file
	logFile, err := os.OpenFile("/tmp/resona_debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		log.SetOutput(logFile)
		log.Printf("DEBUG: Audio player initializing")
	}

	// Initialize speaker with a reasonable sample rate
	sr := beep.SampleRate(44100)
	speaker.Init(sr, sr.N(time.Second/10))

	// Create a mixer for better control
	mixer := &beep.Mixer{}
	speaker.Play(mixer)

	return &AudioPlayer{
		mixer:       mixer,
		isPlaying:   false,
		isPaused:    false,
		speakerInit: true,
	}, nil
}

func (ap *AudioPlayer) Play(filePath string) error {
	// Check if it's a URL or local file
	isURL := strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://")
	
	log.Printf("DEBUG: Playing %s (isURL: %v)", filePath, isURL)
	
	if !isURL && !ap.isFileSupported(filePath) {
		return fmt.Errorf("unsupported file format: %s", filepath.Ext(filePath))
	}

	// Stop any current playback
	ap.Stop()
	
	// If it's a URL, try to play with fallback support
	if isURL {
		return ap.playWithFallback([]string{filePath})
	}
	
	// For local files, use the original logic
	return ap.playDirectly(filePath)
}

// playWithFallback tries multiple URLs until one works
func (ap *AudioPlayer) playWithFallback(urls []string) error {
	var lastError error
	
	for i, url := range urls {
		log.Printf("DEBUG: Trying URL %d/%d: %s", i+1, len(urls), url)
		err := ap.playDirectly(url)
		if err == nil {
			log.Printf("DEBUG: Successfully connected to URL %d: %s", i+1, url)
			return nil
		}
		
		log.Printf("DEBUG: Failed to connect to URL %d: %v", i+1, err)
		lastError = err
	}
	
	return fmt.Errorf("all stream URLs failed, last error: %w", lastError)
}

// PlayRadioStation plays a radio station with fallback support
func (ap *AudioPlayer) PlayRadioStation(station *RadioStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	
	log.Printf("DEBUG: Playing radio station: %s", station.Name)
	
	// Use StreamURLs if available, otherwise fall back to StreamURL
	var urls []string
	if len(station.StreamURLs) > 0 {
		urls = station.StreamURLs
		log.Printf("DEBUG: Using %d fallback URLs", len(urls))
	} else if station.StreamURL != "" {
		urls = []string{station.StreamURL}
		log.Printf("DEBUG: Using single URL: %s", station.StreamURL)
	} else {
		urls = []string{station.URL}
		log.Printf("DEBUG: Using original URL: %s", station.URL)
	}
	
	return ap.playWithFallback(urls)
}

// playDirectly plays a single URL or file without fallback
func (ap *AudioPlayer) playDirectly(filePath string) error {
	// Check if it's a URL or local file
	isURL := strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://")

	// Open the audio source (file or URL)
	var reader io.ReadCloser
	var contentType string
	var err error
	
	if isURL {
		log.Printf("DEBUG: Setting up HTTP client for streaming")
		// Handle HTTP stream with proper configuration for continuous streaming
		client := &http.Client{
			// No timeout for the client itself - streams need to be continuous
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second, // Only timeout for getting headers
				ExpectContinueTimeout: 1 * time.Second,
				DisableKeepAlives:     false, // Keep connections alive
			},
		}
		
		req, err := http.NewRequest("GET", filePath, nil)
		if err != nil {
			log.Printf("DEBUG: Failed to create request: %v", err)
			return fmt.Errorf("failed to create request: %w", err)
		}
		
		// Add headers that mimic a real browser to avoid 403 errors
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Accept-Encoding", "identity") // Don't request compression for audio streams
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Referer", "https://somafm.com/") // Add referer header
		req.Header.Set("Origin", "https://somafm.com")
		req.Header.Set("Sec-Fetch-Dest", "audio")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		// Remove Icy-MetaData for now as it might trigger detection
		// req.Header.Set("Icy-MetaData", "1") // Request ICY metadata
		req.Header.Set("Cache-Control", "no-cache")
		req.Header.Set("Pragma", "no-cache")
		
		log.Printf("DEBUG: Making HTTP request to %s", filePath)
		log.Printf("DEBUG: Request headers: %+v", req.Header)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("DEBUG: HTTP request failed: %v", err)
			return fmt.Errorf("failed to connect to stream: %w", err)
		}
		log.Printf("DEBUG: HTTP response status: %d %s", resp.StatusCode, resp.Status)
		log.Printf("DEBUG: Response headers: %+v", resp.Header)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("stream returned status %d: %s", resp.StatusCode, resp.Status)
		}
		// Wrap the response body with a buffered reader for better stream handling
		log.Printf("DEBUG: Setting up buffered reader")
		bufferedReader := bufio.NewReaderSize(resp.Body, 32*1024) // 32KB buffer
		reader = &bufferedHTTPReader{
			reader: bufferedReader,
			closer: resp.Body,
		}
		
		// Store content type for format detection
		contentType = resp.Header.Get("Content-Type")
		log.Printf("DEBUG: Content-Type: %s", contentType)
	} else {
		// Handle local file
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		reader = file
	}

	// Decode based on file type or content type
	var streamer beep.StreamSeekCloser
	var format beep.Format
	
	log.Printf("DEBUG: Starting audio decoding")
	if isURL {
		// For URLs, try to detect format from Content-Type or assume MP3
		// Try to decode based on content type, fallback to MP3
		if strings.Contains(contentType, "ogg") || strings.Contains(contentType, "vorbis") {
			log.Printf("DEBUG: Decoding as OGG/Vorbis")
			streamer, format, err = vorbis.Decode(reader)
		} else {
			// Default to MP3 for most radio streams
			log.Printf("DEBUG: Decoding as MP3")
			streamer, format, err = mp3.Decode(reader)
		}
	} else {
		// For local files, use extension
		ext := strings.ToLower(filepath.Ext(filePath))
		log.Printf("DEBUG: Decoding local file with extension: %s", ext)
		switch ext {
		case ".mp3":
			streamer, format, err = mp3.Decode(reader)
		case ".wav":
			streamer, format, err = wav.Decode(reader)
		case ".flac":
			streamer, format, err = flac.Decode(reader)
		case ".ogg":
			streamer, format, err = vorbis.Decode(reader)
		default:
			reader.Close()
			return fmt.Errorf("unsupported format: %s", ext)
		}
	}

	if err != nil {
		log.Printf("DEBUG: Audio decoding failed: %v", err)
		reader.Close()
		return err
	}
	log.Printf("DEBUG: Audio decoding successful, format: %+v", format)

	// Resample if necessary
	log.Printf("DEBUG: Resampling from %v to 44100", format.SampleRate)
	resampled := beep.Resample(4, format.SampleRate, beep.SampleRate(44100), streamer)

	// Wrap with sample capture streamer for visualization
	log.Printf("DEBUG: Setting up sample capture streamer")
	sampleCapture := NewSampleCaptureStreamer(resampled, ap)

	// Create a control wrapper for pause/resume
	log.Printf("DEBUG: Setting up audio control")
	ap.mutex.Lock()
	ap.ctrl = &beep.Ctrl{Streamer: sampleCapture, Paused: false}
	ap.isPlaying = true
	ap.isPaused = false
	ap.currentSong = filePath
	ap.startTime = time.Now()
	ap.pausedTime = 0
	ap.mutex.Unlock()

	// Add to mixer with callback for cleanup
	log.Printf("DEBUG: Adding to mixer")
	ap.mixer.Add(beep.Seq(ap.ctrl, beep.Callback(func() {
		log.Printf("DEBUG: Playback finished, cleaning up")
		ap.mutex.Lock()
		ap.isPlaying = false
		ap.isPaused = false
		ap.mutex.Unlock()
		streamer.Close()
		reader.Close()
	})))

	log.Printf("DEBUG: Audio playback started successfully")
	return nil
}

func (ap *AudioPlayer) Pause() {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	if ap.ctrl != nil && ap.isPlaying && !ap.isPaused {
		speaker.Lock()
		ap.ctrl.Paused = true
		ap.isPaused = true
		// Add elapsed time to paused time tracking
		elapsed := time.Since(ap.startTime)
		ap.pausedTime += elapsed
		ap.startTime = time.Now() // Reset start time for next resume
		speaker.Unlock()
	}
}

func (ap *AudioPlayer) Resume() {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	if ap.ctrl != nil && ap.isPlaying && ap.isPaused {
		speaker.Lock()
		ap.ctrl.Paused = false
		ap.isPaused = false
		ap.startTime = time.Now() // Reset start time for resume
		speaker.Unlock()
	}
}

func (ap *AudioPlayer) TogglePause() {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	if ap.ctrl != nil && ap.isPlaying {
		speaker.Lock()
		if ap.isPaused {
			// Currently paused, resume
			ap.ctrl.Paused = false
			ap.isPaused = false
			ap.startTime = time.Now() // Reset start time for resume
		} else {
			// Currently playing, pause
			ap.ctrl.Paused = true
			ap.isPaused = true
			// Add elapsed time to paused time tracking
			elapsed := time.Since(ap.startTime)
			ap.pausedTime += elapsed
			ap.startTime = time.Now() // Reset start time for next resume
		}
		speaker.Unlock()
	}
}

func (ap *AudioPlayer) Stop() {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()

	if ap.ctrl != nil {
		speaker.Lock()
		ap.ctrl.Streamer = nil
		speaker.Unlock()
		ap.ctrl = nil
	}
	
	// Clear the mixer
	speaker.Lock()
	ap.mixer.Clear()
	speaker.Unlock()
	
	ap.isPlaying = false
	ap.isPaused = false
	ap.currentSong = ""
}

func (ap *AudioPlayer) IsPlaying() bool {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	
	if ap.ctrl != nil {
		speaker.Lock()
		actualPaused := ap.ctrl.Paused
		speaker.Unlock()
		return ap.isPlaying && !actualPaused
	}
	return ap.isPlaying
}

func (ap *AudioPlayer) IsPaused() bool {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	
	if ap.ctrl != nil {
		speaker.Lock()
		actualPaused := ap.ctrl.Paused
		speaker.Unlock()
		return actualPaused
	}
	return false
}

func (ap *AudioPlayer) CurrentSong() string {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	return ap.currentSong
}

func (ap *AudioPlayer) isFileSupported(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext == ".mp3" || ext == ".wav" || ext == ".flac" || ext == ".ogg"
}


func (ap *AudioPlayer) SetDuration(duration float64) {
	ap.mutex.Lock()
	defer ap.mutex.Unlock()
	ap.duration = duration
}

func (ap *AudioPlayer) GetPosition() float64 {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	
	// Return 0 only if not playing AND not paused (i.e., stopped)
	if !ap.isPlaying && !ap.isPaused {
		return 0
	}
	
	var elapsed time.Duration
	if ap.isPaused {
		elapsed = ap.pausedTime
	} else {
		elapsed = ap.pausedTime + time.Since(ap.startTime)
	}
	
	return elapsed.Seconds()
}

func (ap *AudioPlayer) GetDuration() float64 {
	ap.mutex.RLock()
	defer ap.mutex.RUnlock()
	return ap.duration
}

func (ap *AudioPlayer) GetProgress() float64 {
	position := ap.GetPosition()
	duration := ap.GetDuration()
	
	if duration <= 0 {
		return 0
	}
	
	progress := position / duration
	if progress > 1.0 {
		progress = 1.0
	}
	if progress < 0 {
		progress = 0
	}
	
	return progress
}

func (ap *AudioPlayer) Close() {
	ap.Stop()
	speaker.Close()
}

// GetAudioSamples returns the current audio amplitude data for visualization
func (ap *AudioPlayer) GetAudioSamples() []float64 {
	ap.sampleMutex.RLock()
	defer ap.sampleMutex.RUnlock()
	
	if ap.audioSamples == nil {
		return make([]float64, 24) // Return empty samples if none available
	}
	
	// Return a copy to avoid concurrent access issues
	samples := make([]float64, len(ap.audioSamples))
	copy(samples, ap.audioSamples)
	return samples
}