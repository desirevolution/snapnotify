package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func wave(rate, channels, frames int) []byte {
	var b bytes.Buffer
	put := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	b.WriteString("RIFF")
	put(uint32(36 + frames*channels*2))
	b.WriteString("WAVEfmt ")
	put(uint32(16))
	put(uint16(1))
	put(uint16(channels))
	put(uint32(rate))
	put(uint32(rate * channels * 2))
	put(uint16(channels * 2))
	put(uint16(16))
	b.WriteString("data")
	put(uint32(frames * channels * 2))
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			put(int16(math.Sin(float64(i)*2*math.Pi*880/float64(rate)) * 10000))
		}
	}
	return b.Bytes()
}

func TestMP3MPEG2And25(t *testing.T) {
	for _, rate := range []int{8000, 11025, 12000, 16000, 22050, 24000} {
		for _, channels := range []int{1, 2} {
			name := fmt.Sprintf("mpeg-%d-%dch.mp3", rate, channels)
			t.Run(name, func(t *testing.T) {
				data, err := os.ReadFile("testdata/" + name)
				if err != nil {
					t.Fatal(err)
				}
				pcm, n, err := decodeBytes(t, data, 1)
				if err != nil {
					t.Fatal(err)
				}
				if n < 6500 || n > 8000 || len(pcm) != int(n)*4 {
					t.Fatalf("unexpected resampled duration: frames=%d", n)
				}
				if bytes.Equal(pcm, make([]byte, len(pcm))) {
					t.Fatal("decoded MPEG audio is silent")
				}
			})
		}
	}
}
func decodeBytes(t *testing.T, data []byte, max int) ([]byte, int64, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out bytes.Buffer
	n, err := decodePCM(context.Background(), f, &out, max)
	return out.Bytes(), n, err
}
func TestDecodeFormatsAndResample(t *testing.T) {
	for _, name := range []string{"wav", "mp3", "ogg"} {
		t.Run(name, func(t *testing.T) {
			data := wave(44100, 1, 4410)
			if name != "wav" {
				var err error
				data, err = os.ReadFile("testdata/tone." + name)
				if err != nil {
					t.Fatal(err)
				}
			}
			pcm, n, err := decodeBytes(t, data, 1)
			if err != nil {
				t.Fatal(err)
			}
			if n < 4000 || n > 12000 || len(pcm) != int(n)*4 {
				t.Fatalf("invalid PCM frames=%d bytes=%d", n, len(pcm))
			}
			nonzero := false
			for i := 0; i < len(pcm); i += 4 {
				if !bytes.Equal(pcm[i:i+2], pcm[i+2:i+4]) {
					t.Fatal("mono was not duplicated into stereo")
				}
				if binary.LittleEndian.Uint16(pcm[i:i+2]) != 0 {
					nonzero = true
				}
			}
			if !nonzero {
				t.Fatal("decoded audio is silent")
			}
		})
	}
}
func TestDecodeLimitsAndMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"duration", wave(48000, 2, 48001)}, {"empty", nil}, {"garbage", []byte("not audio")}, {"truncated-wav", wave(48000, 1, 0)[:20]}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := decodeBytes(t, tc.data, 1); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	data, err := os.ReadFile("testdata/unsupported-opus.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = decodeBytes(t, data, 1); err == nil {
		t.Fatal("Opus must not be accepted as Vorbis")
	}
}
func TestLocalPathAndCleanup(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.wav")
	os.WriteFile(outside, wave(48000, 1, 200), 0600)
	if err := os.Symlink(outside, filepath.Join(dir, "escape.wav")); err != nil {
		t.Fatal(err)
	}
	c := defaultConfig()
	c.Audio.Directory = dir
	c.Sounds = map[string]string{"escape": "escape.wav"}
	a := newApp(context.Background(), c, t.TempDir())
	if _, err := a.local(context.Background(), "escape"); err == nil {
		t.Fatal("symlink escaped audio directory")
	}
	if _, err := a.prepare(context.Background(), bytes.NewReader([]byte("invalid")), "bad"); err == nil {
		t.Fatal("expected decode error")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 0 {
		t.Fatal("temporary files leaked")
	}
}
func TestFileSizeLimit(t *testing.T) {
	c := defaultConfig()
	c.Limits.MaxFileSizeMB = 1
	a := newApp(context.Background(), c, t.TempDir())
	_, err := a.prepare(context.Background(), io.LimitReader(zeroReader{}, (1<<20)+1), "large")
	if e, ok := err.(apiError); !ok || e.Code != 413 {
		t.Fatalf("want 413, got %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
