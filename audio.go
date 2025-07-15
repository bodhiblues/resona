package main

import (
	"fmt"
	"io"
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
	
	if !isURL && !ap.isFileSupported(filePath) {
		return fmt.Errorf("unsupported file format: %s", filepath.Ext(filePath))
	}

	// Stop any current playback
	ap.Stop()

	// Open the audio source (file or URL)
	var reader io.ReadCloser
	var contentType string
	var err error
	
	if isURL {
		// Handle HTTP stream with proper headers
		// No timeout for continuous streaming
		client := &http.Client{}
		
		req, err := http.NewRequest("GET", filePath, nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		
		// Add headers that many radio streams require
		req.Header.Set("User-Agent", "Resona/1.0 (Terminal Music Player)")
		req.Header.Set("Accept", "audio/mpeg, audio/ogg, audio/*")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Icy-MetaData", "1") // Request ICY metadata
		
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to connect to stream: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("stream returned status %d", resp.StatusCode)
		}
		reader = resp.Body
		
		// Store content type for format detection
		contentType = resp.Header.Get("Content-Type")
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
	
	if isURL {
		// For URLs, try to detect format from Content-Type or assume MP3
		// Try to decode based on content type, fallback to MP3
		if strings.Contains(contentType, "ogg") || strings.Contains(contentType, "vorbis") {
			streamer, format, err = vorbis.Decode(reader)
		} else {
			// Default to MP3 for most radio streams
			streamer, format, err = mp3.Decode(reader)
		}
	} else {
		// For local files, use extension
		ext := strings.ToLower(filepath.Ext(filePath))
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
		reader.Close()
		return err
	}

	// Resample if necessary
	resampled := beep.Resample(4, format.SampleRate, beep.SampleRate(44100), streamer)

	// Wrap with sample capture streamer for visualization
	sampleCapture := NewSampleCaptureStreamer(resampled, ap)

	// Create a control wrapper for pause/resume
	ap.mutex.Lock()
	ap.ctrl = &beep.Ctrl{Streamer: sampleCapture, Paused: false}
	ap.isPlaying = true
	ap.isPaused = false
	ap.currentSong = filePath
	ap.startTime = time.Now()
	ap.pausedTime = 0
	ap.mutex.Unlock()

	// Add to mixer with callback for cleanup
	ap.mixer.Add(beep.Seq(ap.ctrl, beep.Callback(func() {
		ap.mutex.Lock()
		ap.isPlaying = false
		ap.isPaused = false
		ap.mutex.Unlock()
		streamer.Close()
		reader.Close()
	})))

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