package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/vorbis"
	"github.com/gopxl/beep/v2/wav"
)

const sampleRate = 48000
const bytesPerSecond = sampleRate * 4

type Job struct {
	ID                string  `json:"id"`
	Label             string  `json:"label"`
	DurationSeconds   float64 `json:"duration_seconds"`
	Path              string  `json:"-"`
	SavedSound        *Sound  `json:"saved_sound,omitempty"`
	originalPath      string
	originalExtension string
}

func (j Job) cleanup() {
	if j.Path != "" {
		os.Remove(j.Path)
	}
	if j.originalPath != "" {
		os.Remove(j.originalPath)
	}
}

func (a *App) local(ctx context.Context, id string) (Job, error) {
	name, ok := a.cfg.Sounds[id]
	if !ok && a.cfg.Audio.ScanDirectory {
		sounds, err := a.sounds()
		if err != nil {
			return Job{}, err
		}
		for _, sound := range sounds {
			if sound.ID == id {
				name, ok = sound.Filename, true
				break
			}
		}
	}
	if !ok {
		return Job{}, apiError{404, "unknown sound ID"}
	}
	// os.Root also prevents symlink traversal outside the sound directory.
	root, err := os.OpenRoot(a.cfg.Audio.Directory)
	if err != nil {
		return Job{}, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return Job{}, fmt.Errorf("sound file unavailable: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Job{}, err
	}
	if !info.Mode().IsRegular() {
		return Job{}, errors.New("sound must be a regular file")
	}
	return a.prepare(ctx, f, id)
}

func (a *App) prepare(ctx context.Context, source io.Reader, label string) (job Job, err error) {
	return a.prepareInput(ctx, source, label, false)
}

func (a *App) prepareInput(ctx context.Context, source io.Reader, label string, keepOriginal bool) (job Job, err error) {
	input, err := os.CreateTemp(a.tempDir, "input-*")
	if err != nil {
		return job, err
	}
	defer func() {
		input.Close()
		if err != nil || !keepOriginal {
			os.Remove(input.Name())
		}
	}()
	n, err := io.Copy(input, io.LimitReader(&contextReader{ctx, source}, a.cfg.maxBytes()+1))
	if err != nil {
		return job, err
	}
	if n > a.cfg.maxBytes() {
		return job, apiError{413, "file size limit exceeded"}
	}
	if n == 0 {
		return job, apiError{422, "empty audio file"}
	}
	if _, err = input.Seek(0, 0); err != nil {
		return job, err
	}
	header := make([]byte, 12)
	io.ReadFull(input, header)
	input.Seek(0, 0)
	ext := ".mp3"
	if bytes.Equal(header[:4], []byte("RIFF")) {
		ext = ".wav"
	}
	if bytes.Equal(header[:4], []byte("OggS")) {
		ext = ".ogg"
	}
	output, err := os.CreateTemp(a.tempDir, "pcm-*")
	if err != nil {
		return job, err
	}
	defer func() {
		output.Close()
		if err != nil {
			os.Remove(output.Name())
		}
	}()
	frames, err := decodePCM(ctx, input, output, a.cfg.Limits.MaxAudioDurationSeconds)
	if err != nil {
		return job, apiError{422, err.Error()}
	}
	job = Job{ID: newID(), Label: label, DurationSeconds: float64(frames) / sampleRate, Path: output.Name()}
	if keepOriginal {
		job.originalPath = input.Name()
		job.originalExtension = ext
	}
	return job, nil
}

// Decode without importing beep/speaker: no Oto, ALSA, CGO or audio device.
func decodePCM(ctx context.Context, input *os.File, out io.Writer, maxSeconds int) (frames int64, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = errors.New("audio decoder rejected malformed file")
		}
	}()
	header := make([]byte, 12)
	n, _ := io.ReadFull(input, header)
	input.Seek(0, 0)
	var decoder interface {
		beep.Streamer
		Close() error
	}
	var format beep.Format
	switch {
	case n >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WAVE")):
		decoder, format, err = wav.Decode(input)
	case n >= 4 && bytes.Equal(header[:4], []byte("OggS")):
		decoder, format, err = vorbis.Decode(input)
	case n >= 3 && (bytes.Equal(header[:3], []byte("ID3")) || header[0] == 0xff && header[1]&0xe0 == 0xe0):
		decoder, format, err = decodeMP3(ctx, input)
	default:
		return 0, errors.New("unsupported audio: use MP3, PCM-WAV or Ogg Vorbis (not Opus)")
	}
	if err != nil {
		return 0, fmt.Errorf("cannot decode audio: %w", err)
	}
	defer decoder.Close()
	if format.SampleRate < 8000 || format.SampleRate > 192000 {
		return 0, errors.New("sample rate must be 8000..192000 Hz")
	}
	var stream beep.Streamer = decoder
	if format.SampleRate != sampleRate {
		stream = beep.Resample(4, format.SampleRate, sampleRate, decoder)
	}
	samples := make([][2]float64, 2048)
	pcm := make([]byte, len(samples)*4)
	for {
		if err = ctx.Err(); err != nil {
			return frames, err
		}
		n, ok := stream.Stream(samples)
		if frames+int64(n) > int64(maxSeconds)*sampleRate {
			return frames, errors.New("audio duration limit exceeded")
		}
		for i := 0; i < n; i++ {
			for ch := 0; ch < 2; ch++ {
				v := samples[i][ch]
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return frames, errors.New("invalid audio samples")
				}
				v = math.Max(-1, math.Min(1, v))
				value := int16(math.Round(v * 32767))
				if v <= -1 {
					value = -32768
				}
				binary.LittleEndian.PutUint16(pcm[i*4+ch*2:], uint16(value))
			}
		}
		if _, err = out.Write(pcm[:n*4]); err != nil {
			return frames, err
		}
		frames += int64(n)
		if !ok {
			break
		}
		if n == 0 {
			return frames, errors.New("decoder produced no audio")
		}
	}
	if err = stream.Err(); err != nil {
		return frames, err
	}
	if frames == 0 {
		return 0, errors.New("audio contains no samples")
	}
	return frames, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func audioURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, apiError{400, "expected HTTP/HTTPS URL without embedded credentials"}
	}
	return u, nil
}

func (a *App) fromURL(ctx context.Context, raw string, save bool) (Job, error) {
	u, err := audioURL(raw)
	if err != nil {
		return Job{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(a.cfg.Limits.DownloadTimeoutSeconds)*time.Second)
	defer cancel()
	client := &http.Client{Timeout: time.Duration(a.cfg.Limits.DownloadTimeoutSeconds) * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		_, err := audioURL(req.URL.String())
		return err
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return Job{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return Job{}, apiError{502, "audio download failed or timed out"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Job{}, apiError{502, fmt.Sprintf("audio download returned HTTP %d", resp.StatusCode)}
	}
	if resp.ContentLength > a.cfg.maxBytes() {
		return Job{}, apiError{413, "file size limit exceeded"}
	}
	label := u.Hostname() + "/" + filepath.Base(strings.TrimSuffix(u.Path, "/"))
	return a.prepareInput(ctx, resp.Body, label, save)
}
