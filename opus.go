package main

// Opus playback support.
//
// beep has no Opus decoder, so this file supplies one built on pion/opus (a
// pure-Go implementation, so it adds no C dependency to the build) wrapped in
// the beep.StreamSeekCloser interface the rest of the player expects.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/gopxl/beep/v2"
	"github.com/pion/opus"
	"github.com/pion/opus/pkg/oggreader"
)

// Opus always decodes at 48kHz, and Ogg granule positions for an Opus stream
// count 48kHz samples whatever the rate of the original material (RFC 7845 §4).
const opusSampleRate = 48000

// The largest number of samples per channel one Opus packet can yield: a
// 120ms frame at 48kHz.
const maxOpusFrameSamples = 5760

// opusStreamer decodes an Ogg Opus file on demand rather than up front, so
// starting a track costs one packet rather than the whole file.
type opusStreamer struct {
	rc io.ReadCloser
	rs io.ReadSeeker

	ogg *oggreader.OggReader
	dec opus.Decoder

	channels int
	preSkip  int
	length   int // total samples per channel, excluding the pre-skip

	buf    []float32 // interleaved PCM from the most recent packet
	bufLen int       // valid samples per channel in buf
	bufPos int       // read cursor within buf, in samples per channel

	pos int
	err error
}

// decodeOpus mirrors the signature of beep's own decoders (mp3.Decode and
// friends) so the switch in playDirectly reads the same for every format.
func decodeOpus(rc io.ReadCloser) (beep.StreamSeekCloser, beep.Format, error) {
	// Seeking the progress bar means re-reading the file, so a plain stream
	// won't do. Local playback always hands us an *os.File, so this only
	// rejects sources that could never have supported seeking anyway.
	rs, ok := rc.(io.ReadSeeker)
	if !ok {
		return nil, beep.Format{}, errors.New("opus: source is not seekable")
	}

	s := &opusStreamer{rc: rc, rs: rs}
	if err := s.reset(); err != nil {
		return nil, beep.Format{}, err
	}

	length, err := opusTotalSamples(rs, s.preSkip)
	if err != nil {
		return nil, beep.Format{}, err
	}
	s.length = length

	// Reading the tail moved the file offset, so start over from the top.
	if err := s.reset(); err != nil {
		return nil, beep.Format{}, err
	}

	format := beep.Format{
		SampleRate:  beep.SampleRate(opusSampleRate),
		NumChannels: s.channels,
		Precision:   2,
	}
	return s, format, nil
}

// reset returns the stream to its first audio sample, rebuilding the Ogg
// reader and decoder. Seeking backwards is built on this.
func (s *opusStreamer) reset() error {
	if _, err := s.rs.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("opus: rewind: %w", err)
	}

	ogg, header, err := oggreader.NewWith(s.rs)
	if err != nil {
		return fmt.Errorf("opus: read ogg header: %w", err)
	}

	channels := int(header.Channels)
	if channels != 1 && channels != 2 {
		return fmt.Errorf("opus: unsupported channel count %d", channels)
	}

	dec, err := opus.NewDecoderWithOutput(opusSampleRate, channels)
	if err != nil {
		return fmt.Errorf("opus: init decoder: %w", err)
	}

	s.ogg = ogg
	s.dec = dec
	s.channels = channels
	s.preSkip = int(header.PreSkip)
	s.buf = make([]float32, maxOpusFrameSamples*channels)
	s.bufLen, s.bufPos, s.err = 0, 0, nil

	// The encoder's warm-up samples are not audio; RFC 7845 §4.2 requires the
	// decoder to drop them, otherwise every track opens with a click.
	if err := s.drop(s.preSkip); err != nil {
		return err
	}
	s.pos = 0
	return nil
}

// fill decodes the next audio packet into buf, reporting false once the stream
// ends or a fatal error occurs.
func (s *opusStreamer) fill() bool {
	for {
		packet, _, err := s.ogg.ParseNextPacket()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.err = err
			}
			return false
		}

		// The identification and comment packets carry no audio.
		if bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
			continue
		}

		n, err := s.dec.DecodeToFloat32(packet, s.buf)
		if err != nil || n == 0 {
			// One damaged packet is 20ms of audio. Skip it and keep playing
			// rather than dropping the track, which is what a hardware player
			// would do.
			continue
		}

		s.bufLen, s.bufPos = n, 0
		return true
	}
}

// drop consumes n samples per channel without emitting them. It leaves pos
// alone; callers adjust it themselves.
func (s *opusStreamer) drop(n int) error {
	for n > 0 {
		if s.bufPos >= s.bufLen && !s.fill() {
			return s.err // nil at end of stream
		}
		take := s.bufLen - s.bufPos
		if take > n {
			take = n
		}
		s.bufPos += take
		n -= take
	}
	return nil
}

func (s *opusStreamer) Stream(samples [][2]float64) (int, bool) {
	filled := 0
	for filled < len(samples) {
		// The final packet decodes to a whole frame, which usually overshoots
		// the granule position by a few milliseconds of encoder padding.
		// RFC 7845 §4.4 says to trim to the granule, so playback ends exactly
		// where Len reports it does.
		if s.length > 0 && s.pos >= s.length {
			break
		}
		if s.bufPos >= s.bufLen && !s.fill() {
			break
		}
		for s.bufPos < s.bufLen && filled < len(samples) {
			if s.length > 0 && s.pos >= s.length {
				break
			}
			i := s.bufPos * s.channels
			left := float64(s.buf[i])
			right := left // mono plays out of both speakers
			if s.channels == 2 {
				right = float64(s.buf[i+1])
			}
			samples[filled][0], samples[filled][1] = left, right
			filled++
			s.bufPos++
			s.pos++
		}
	}
	return filled, filled > 0
}

func (s *opusStreamer) Err() error    { return s.err }
func (s *opusStreamer) Len() int      { return s.length }
func (s *opusStreamer) Position() int { return s.pos }
func (s *opusStreamer) Close() error  { return s.rc.Close() }

// Seek moves playback to sample p. Ogg Opus carries no seek index, so getting
// there means decoding up to it: a forward seek continues from the current
// position, and only a backward one starts the file again. Decoding runs a
// couple of hundred times faster than playback, so even a full rewind of a
// long track costs about a second.
func (s *opusStreamer) Seek(p int) error {
	if p < 0 || p > s.length {
		return fmt.Errorf("opus: seek position %d out of range [0, %d]", p, s.length)
	}
	if p < s.pos {
		if err := s.reset(); err != nil {
			return err
		}
	}
	if err := s.drop(p - s.pos); err != nil {
		return err
	}
	s.pos = p
	return nil
}

// opusTotalSamples reports the playable length in samples per channel, read
// from the granule position of the final Ogg page. That counts 48kHz samples
// from the start of the stream including the pre-skip, so the pre-skip comes
// back off. This reads a few KB from the end of the file instead of decoding
// it, which is what makes scanning a large library affordable.
func opusTotalSamples(rs io.ReadSeeker, preSkip int) (int, error) {
	size, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("opus: measure file: %w", err)
	}

	// The final page is normally within a few KB of the end; widen the window
	// if an unusually large last packet pushes it further back.
	const maxWindow = 1 << 20
	for window := int64(64 << 10); ; window *= 4 {
		if window > maxWindow {
			window = maxWindow
		}
		start := size - window
		if start < 0 {
			start = 0
		}
		if _, err := rs.Seek(start, io.SeekStart); err != nil {
			return 0, fmt.Errorf("opus: seek to tail: %w", err)
		}
		tail := make([]byte, size-start)
		if _, err := io.ReadFull(rs, tail); err != nil {
			return 0, fmt.Errorf("opus: read tail: %w", err)
		}

		if granule, ok := lastGranulePosition(tail); ok {
			total := int(granule) - preSkip
			if total < 0 {
				total = 0
			}
			return total, nil
		}
		if start == 0 || window >= maxWindow {
			return 0, errors.New("opus: no ogg page header found near end of file")
		}
	}
}

// lastGranulePosition finds the granule position of the last Ogg page starting
// in buf. "OggS" can occur inside packet payloads too, so candidates are
// checked against the version byte, and a page flagged end-of-stream wins
// outright since that is the one whose granule marks the end of the stream.
func lastGranulePosition(buf []byte) (uint64, bool) {
	const headerSize = 27 // through the page_segments field

	var granule uint64
	var found bool

	for offset := 0; ; {
		i := bytes.Index(buf[offset:], []byte("OggS"))
		if i < 0 {
			break
		}
		at := offset + i
		offset = at + 4

		if at+headerSize > len(buf) {
			continue
		}
		if buf[at+4] != 0 { // stream_structure_version is always 0
			continue
		}

		g := binary.LittleEndian.Uint64(buf[at+6 : at+14])
		granule, found = g, true

		if buf[at+5]&0x04 != 0 { // end-of-stream flag
			return g, true
		}
	}
	return granule, found
}

// calculateOpusDuration returns a track's length in seconds without decoding
// it, for the library scanner.
func calculateOpusDuration(filePath string) float64 {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	_, header, err := oggreader.NewWith(file)
	if err != nil {
		return 0
	}

	total, err := opusTotalSamples(file, int(header.PreSkip))
	if err != nil {
		return 0
	}
	return float64(total) / float64(opusSampleRate)
}
