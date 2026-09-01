package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"testing"

	"github.com/gopxl/beep/v2"
)

// The fixtures in testdata are pure tones encoded with libopus at 48kHz.
// tone-stereo carries a different frequency in each channel so that a test can
// tell a genuine stereo stream from a duplicated or swapped one.
const (
	stereoFixture = "testdata/tone-stereo.opus"
	monoFixture   = "testdata/tone-mono.opus"
	shortFixture  = "testdata/tone-short.opus"

	stereoLeftHz  = 440
	stereoRightHz = 1760
	monoHz        = 440
)

// Lengths are exact: libopus wrote a 312-sample pre-skip, which the decoder is
// required to drop, so a 3s tone is exactly 144000 samples of audio. ffprobe
// reports these files 6.5ms longer because it does not subtract the pre-skip.
var fixtureSamples = map[string]int{
	stereoFixture: 3 * opusSampleRate,
	monoFixture:   2 * opusSampleRate,
	shortFixture:  0.3 * opusSampleRate,
}

func openFixture(t *testing.T, path string) beep.StreamSeekCloser {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	s, _, err := decodeOpus(f)
	if err != nil {
		f.Close()
		t.Fatalf("decodeOpus(%s): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// readSamples pulls exactly n samples, or as many as remain.
func readSamples(s beep.StreamSeekCloser, n int) [][2]float64 {
	out := make([][2]float64, 0, n)
	buf := make([][2]float64, 4096)
	for len(out) < n {
		want := buf
		if r := n - len(out); r < len(want) {
			want = want[:r]
		}
		got, ok := s.Stream(want)
		out = append(out, want[:got]...)
		if !ok {
			break
		}
	}
	return out
}

// cyclesPerSecond estimates a tone's frequency by counting how often the
// waveform swings from clearly negative to clearly positive. The threshold
// keeps codec noise near the zero line from being counted as a crossing.
func cyclesPerSecond(samples [][2]float64, channel int) float64 {
	const threshold = 0.1
	state, cycles := 0, 0
	for _, s := range samples {
		v := s[channel]
		if v > threshold {
			if state == -1 {
				cycles++
			}
			state = 1
		} else if v < -threshold {
			state = -1
		}
	}
	return float64(cycles) / (float64(len(samples)) / float64(opusSampleRate))
}

func TestOpusDurationSubtractsPreSkip(t *testing.T) {
	for path, want := range fixtureSamples {
		got := calculateOpusDuration(path)
		wantSecs := float64(want) / float64(opusSampleRate)
		if math.Abs(got-wantSecs) > 0.001 {
			t.Errorf("%s: duration = %.4fs, want %.4fs", path, got, wantSecs)
		}
	}
}

func TestOpusLengthIsExact(t *testing.T) {
	for path, want := range fixtureSamples {
		s := openFixture(t, path)
		if s.Len() != want {
			t.Errorf("%s: Len = %d, want %d", path, s.Len(), want)
		}

		// Draining must land exactly on Len: the pre-skip comes off the front
		// and the final packet's padding is trimmed off the back.
		got := len(readSamples(s, want*2))
		if got != want {
			t.Errorf("%s: streamed %d samples, want %d", path, got, want)
		}
		if s.Err() != nil {
			t.Errorf("%s: Err = %v", path, s.Err())
		}
		if s.Position() != want {
			t.Errorf("%s: Position after drain = %d, want %d", path, s.Position(), want)
		}
	}
}

func TestOpusStereoChannelsAreIndependent(t *testing.T) {
	s := openFixture(t, stereoFixture)

	// Skip the first half-second so the encoder's ramp-up is behind us.
	if err := s.Seek(opusSampleRate / 2); err != nil {
		t.Fatal(err)
	}
	samples := readSamples(s, opusSampleRate)
	if len(samples) != opusSampleRate {
		t.Fatalf("got %d samples, want %d", len(samples), opusSampleRate)
	}

	left := cyclesPerSecond(samples, 0)
	right := cyclesPerSecond(samples, 1)
	t.Logf("left %.0fHz, right %.0fHz", left, right)

	if math.Abs(left-stereoLeftHz) > stereoLeftHz*0.05 {
		t.Errorf("left channel %.0fHz, want ~%dHz", left, stereoLeftHz)
	}
	if math.Abs(right-stereoRightHz) > stereoRightHz*0.05 {
		t.Errorf("right channel %.0fHz, want ~%dHz — channels may be swapped or duplicated",
			right, stereoRightHz)
	}
}

func TestOpusMonoFeedsBothChannels(t *testing.T) {
	f, err := os.Open(monoFixture)
	if err != nil {
		t.Fatal(err)
	}
	s, format, err := decodeOpus(f)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if format.NumChannels != 1 {
		t.Errorf("NumChannels = %d, want 1", format.NumChannels)
	}
	if format.SampleRate != beep.SampleRate(opusSampleRate) {
		t.Errorf("SampleRate = %d, want %d", format.SampleRate, opusSampleRate)
	}

	samples := readSamples(s, opusSampleRate)
	if len(samples) == 0 {
		t.Fatal("no samples")
	}
	for i, smp := range samples {
		if smp[0] != smp[1] {
			t.Fatalf("mono sample %d not mirrored: L=%v R=%v", i, smp[0], smp[1])
		}
	}
	if hz := cyclesPerSecond(samples, 0); math.Abs(hz-monoHz) > monoHz*0.05 {
		t.Errorf("mono tone %.0fHz, want ~%dHz", hz, monoHz)
	}
}

func TestOpusSeekIsRepeatable(t *testing.T) {
	s := openFixture(t, stereoFixture)
	const target = opusSampleRate // 1s in

	if err := s.Seek(target); err != nil { // forward from the start
		t.Fatal(err)
	}
	if s.Position() != target {
		t.Errorf("Position = %d, want %d", s.Position(), target)
	}
	forward := readSamples(s, 2048)

	if err := s.Seek(target); err != nil { // backward, forcing a restart
		t.Fatal(err)
	}
	backward := readSamples(s, 2048)

	if len(forward) != len(backward) || len(forward) == 0 {
		t.Fatalf("sample counts differ: %d vs %d", len(forward), len(backward))
	}
	for i := range forward {
		if forward[i] != backward[i] {
			t.Fatalf("sample %d differs after re-seek: %v vs %v", i, forward[i], backward[i])
		}
	}
}

func TestOpusSeekBounds(t *testing.T) {
	s := openFixture(t, stereoFixture)

	if err := s.Seek(-1); err == nil {
		t.Error("Seek(-1) should fail")
	}
	if err := s.Seek(s.Len() + 1); err == nil {
		t.Error("Seek past end should fail")
	}
	if err := s.Seek(s.Len()); err != nil {
		t.Errorf("Seek to exactly Len should succeed: %v", err)
	}
	if n := len(readSamples(s, 128)); n != 0 {
		t.Errorf("streamed %d samples after seeking to the end, want 0", n)
	}
	if err := s.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	if s.Position() != 0 {
		t.Errorf("Position = %d after Seek(0), want 0", s.Position())
	}
}

func TestOpusRejectsNonSeekableSource(t *testing.T) {
	data, err := os.ReadFile(stereoFixture)
	if err != nil {
		t.Fatal(err)
	}
	// Embedding io.Reader rather than *bytes.Reader hides the Seek method.
	rc := struct {
		io.Reader
		io.Closer
	}{bytes.NewReader(data), io.NopCloser(nil)}

	if _, _, err := decodeOpus(rc); err == nil {
		t.Error("expected an error for a source that cannot seek")
	}
}

func TestOpusIsRecognisedAsSupported(t *testing.T) {
	if !isSupportedAudio(stereoFixture) {
		t.Error("library scanner does not treat .opus as supported")
	}
	ap := &AudioPlayer{}
	if !ap.isFileSupported(stereoFixture) {
		t.Error("player does not treat .opus as supported")
	}
	if got := calculateDuration(stereoFixture); math.Abs(got-3) > 0.001 {
		t.Errorf("calculateDuration = %.4f, want 3.0 — .opus may be missing from the dispatch", got)
	}
}

func TestOpusMetadataIsRead(t *testing.T) {
	song := extractMetadata(stereoFixture)
	if song.Artist != "Test Artist" {
		t.Errorf("Artist = %q, want %q", song.Artist, "Test Artist")
	}
	if song.Title != "Test Tone" {
		t.Errorf("Title = %q, want %q", song.Title, "Test Tone")
	}
	if song.Album != "Test Album" {
		t.Errorf("Album = %q, want %q", song.Album, "Test Album")
	}
	if song.TrackNumber != 7 {
		t.Errorf("TrackNumber = %d, want 7", song.TrackNumber)
	}
	if math.Abs(song.DurationSecs-3) > 0.001 {
		t.Errorf("DurationSecs = %.4f, want 3.0", song.DurationSecs)
	}
}

// oggPage builds a minimal page header for the granule-scanning tests.
func oggPage(granule uint64, eos bool, version byte) []byte {
	p := make([]byte, 27)
	copy(p, "OggS")
	p[4] = version
	if eos {
		p[5] = 0x04
	}
	binary.LittleEndian.PutUint64(p[6:14], granule)
	return p
}

func TestLastGranulePosition(t *testing.T) {
	t.Run("prefers the end-of-stream page", func(t *testing.T) {
		buf := append(oggPage(100, false, 0), oggPage(999, true, 0)...)
		buf = append(buf, oggPage(12345, false, 0)...) // a later page without EOS
		got, ok := lastGranulePosition(buf)
		if !ok || got != 999 {
			t.Errorf("got (%d, %v), want (999, true)", got, ok)
		}
	})

	t.Run("falls back to the last valid page", func(t *testing.T) {
		buf := append(oggPage(100, false, 0), oggPage(200, false, 0)...)
		got, ok := lastGranulePosition(buf)
		if !ok || got != 200 {
			t.Errorf("got (%d, %v), want (200, true)", got, ok)
		}
	})

	t.Run("ignores OggS appearing inside payload data", func(t *testing.T) {
		// A page whose payload happens to contain the capture pattern, with a
		// non-zero version byte marking it as not a real header.
		buf := append(oggPage(500, false, 0), oggPage(7777, false, 9)...)
		got, ok := lastGranulePosition(buf)
		if !ok || got != 500 {
			t.Errorf("got (%d, %v), want (500, true)", got, ok)
		}
	})

	t.Run("reports nothing when no page is present", func(t *testing.T) {
		if _, ok := lastGranulePosition([]byte("no pages here at all")); ok {
			t.Error("expected ok=false")
		}
	})

	t.Run("ignores a truncated trailing header", func(t *testing.T) {
		buf := append(oggPage(400, false, 0), []byte("OggS\x00")...)
		got, ok := lastGranulePosition(buf)
		if !ok || got != 400 {
			t.Errorf("got (%d, %v), want (400, true)", got, ok)
		}
	})
}

// TestOpusDropsPreSkipPadding guards the pre-skip handling directly. The
// fixtures are tones that begin at full amplitude, so if the encoder's warm-up
// samples were played instead of dropped the stream would open on digital
// silence — which is the click RFC 7845 §4.2 exists to prevent. Length and
// duration checks cannot catch this: the granule arithmetic stays correct and
// the end-trim hides the overrun, leaving only the 6.5ms offset at the front.
func TestOpusDropsPreSkipPadding(t *testing.T) {
	for _, path := range []string{stereoFixture, monoFixture} {
		s := openFixture(t, path)
		samples := readSamples(s, 50)
		if len(samples) != 50 {
			t.Fatalf("%s: got %d samples, want 50", path, len(samples))
		}

		var sum float64
		for _, smp := range samples {
			sum += smp[0] * smp[0]
		}
		rms := math.Sqrt(sum / float64(len(samples)))

		// A correctly trimmed stream opens at ~0.09 RMS; the padding measures 0.
		if rms < 0.05 {
			t.Errorf("%s: first 50 samples have RMS %.6f — pre-skip padding is being played", path, rms)
		}
	}
}
