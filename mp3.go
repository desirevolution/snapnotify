package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"github.com/gopxl/beep/v2"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
)

// Adapt the pure-Go MPEG-1/2/2.5 decoder to Beep's stereo float interface.
// No speaker package, native library, external process, or CGO is involved.
type mp3Stream struct {
	decoder  *mp3pcm.Decoder
	channels int
	buffer   []byte
	err      error
}

func decodeMP3(ctx context.Context, r io.Reader) (*mp3Stream, beep.Format, error) {
	d, err := mp3pcm.NewDecoder(&contextReader{ctx, r}, mp3pcm.WithF32())
	if err != nil {
		return nil, beep.Format{}, err
	}
	info := d.Info()
	if info.Channels != 1 && info.Channels != 2 {
		return nil, beep.Format{}, errors.New("MP3 must have one or two channels")
	}
	return &mp3Stream{decoder: d, channels: info.Channels}, beep.Format{SampleRate: beep.SampleRate(info.SampleRate), NumChannels: info.Channels, Precision: 4}, nil
}
func (s *mp3Stream) Stream(samples [][2]float64) (int, bool) {
	if s.err != nil {
		return 0, false
	}
	frameBytes := s.channels * 4
	length := len(samples) * frameBytes
	if cap(s.buffer) < length {
		s.buffer = make([]byte, length)
	}
	s.buffer = s.buffer[:length]
	n, err := io.ReadFull(s.decoder, s.buffer)
	if err == io.ErrUnexpectedEOF && n%frameBytes == 0 {
		err = io.EOF
	}
	if n%frameBytes != 0 {
		err = io.ErrUnexpectedEOF
	}
	s.err = err
	frames := n / frameBytes
	for i := 0; i < frames; i++ {
		left := float64(math.Float32frombits(binary.LittleEndian.Uint32(s.buffer[i*frameBytes:])))
		right := left
		if s.channels == 2 {
			right = float64(math.Float32frombits(binary.LittleEndian.Uint32(s.buffer[i*frameBytes+4:])))
		}
		samples[i] = [2]float64{left, right}
	}
	return frames, frames > 0
}
func (s *mp3Stream) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}
func (s *mp3Stream) Close() error { return nil }
