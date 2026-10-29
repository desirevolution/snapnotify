package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleConfig(t *testing.T) {
	c, err := loadConfig("config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.QueueSize != 20 || c.Audio.Volume != 60 || !filepath.IsAbs(c.Audio.Directory) || c.MPD.Address != "" || c.Snapcast.Stream != "notifications" {
		t.Fatal("unexpected config defaults")
	}
}
func TestStrictConfig(t *testing.T) {
	for _, text := range []string{"unexpected: true\n", "limits:\n  queue_size: 0\n", "sounds:\n  bad: ../outside.wav\n", "audio:\n  volume: 101\n", "---\n{}\n---\n{}\n", "mqtt:\n  enabled: true\n  broker: http://broker\n"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(p, []byte(text), 0600)
		if _, err := loadConfig(p); err == nil {
			t.Fatalf("invalid config accepted: %s", text)
		}
	}
}

func TestOptionalMPDConfig(t *testing.T) {
	for _, text := range []string{"{}\n", "mpd:\n  address: \"\"\n"} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(p, []byte(text), 0600)
		c, err := loadConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		if c.MPD.Address != "" {
			t.Fatal("MPD should be disabled without an address")
		}
	}
	c := defaultConfig()
	c.MPD.Address = "invalid"
	if c.validate() == nil {
		t.Fatal("malformed MPD address accepted")
	}
	c.MPD.Address = "mpd:6600"
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}
