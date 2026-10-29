package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	HTTP struct {
		Listen string `yaml:"listen"`
	} `yaml:"http"`
	Audio struct {
		Directory     string `yaml:"directory"`
		Volume        int    `yaml:"volume"`
		ScanDirectory bool   `yaml:"scan_directory"`
	} `yaml:"audio"`
	Snapcast struct {
		ControlAddress string `yaml:"control_address"`
		AudioAddress   string `yaml:"audio_address"`
		Stream         string `yaml:"stream"`
		BufferMS       int    `yaml:"buffer_ms"`
		SettleMS       int    `yaml:"settle_ms"`
	} `yaml:"snapcast"`
	MPD struct {
		Address  string `yaml:"address"`
		Password string `yaml:"password"`
	} `yaml:"mpd"`
	MQTT struct {
		Enabled  bool   `yaml:"enabled"`
		Broker   string `yaml:"broker"`
		Topic    string `yaml:"topic"`
		ClientID string `yaml:"client_id"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"mqtt"`
	Limits struct {
		QueueSize               int   `yaml:"queue_size"`
		MaxFileSizeMB           int64 `yaml:"max_file_size_mb"`
		MaxAudioDurationSeconds int   `yaml:"max_audio_duration_seconds"`
		DownloadTimeoutSeconds  int   `yaml:"download_timeout_seconds"`
	} `yaml:"limits"`
	Sounds map[string]string `yaml:"sounds"`
}

func defaultConfig() Config {
	var c Config
	c.HTTP.Listen = ":8080"
	c.Audio.Directory, c.Audio.Volume = "./sounds", 60
	c.Audio.ScanDirectory = true
	c.Snapcast.ControlAddress, c.Snapcast.AudioAddress = "snapserver:1705", "snapserver:4953"
	c.Snapcast.Stream, c.Snapcast.BufferMS, c.Snapcast.SettleMS = "notifications", 350, 150
	c.MQTT.Topic, c.MQTT.ClientID = "notifications/play", "snapnotify"
	c.Limits.QueueSize, c.Limits.MaxFileSizeMB = 20, 20
	c.Limits.MaxAudioDurationSeconds, c.Limits.DownloadTimeoutSeconds = 120, 30
	c.Sounds = map[string]string{}
	return c
}

func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := yaml.NewDecoder(f)
	d.KnownFields(true)
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return c, errors.New("exactly one YAML document required")
	}
	if err = c.validate(); err != nil {
		return c, err
	}
	if !filepath.IsAbs(c.Audio.Directory) {
		c.Audio.Directory = filepath.Join(filepath.Dir(path), c.Audio.Directory)
	}
	c.Audio.Directory, err = filepath.Abs(c.Audio.Directory)
	return c, err
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func (c Config) validate() error {
	for name, addr := range map[string]string{"http.listen": c.HTTP.Listen, "snapcast.control_address": c.Snapcast.ControlAddress, "snapcast.audio_address": c.Snapcast.AudioAddress} {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("%s: expected host:port", name)
		}
	}
	if c.MPD.Address != "" {
		if _, _, err := net.SplitHostPort(c.MPD.Address); err != nil {
			return errors.New("mpd.address: expected host:port, or empty to disable MPD")
		}
	}
	if c.Audio.Volume < 0 || c.Audio.Volume > 100 {
		return errors.New("audio.volume must be 0..100")
	}
	if c.Audio.Directory == "" || c.Snapcast.Stream == "" {
		return errors.New("audio.directory and snapcast.stream must not be empty")
	}
	if c.Snapcast.BufferMS < 0 || c.Snapcast.BufferMS > 10000 || c.Snapcast.SettleMS < 0 || c.Snapcast.SettleMS > 10000 {
		return errors.New("buffer_ms and settle_ms must be 0..10000")
	}
	if c.Limits.QueueSize < 1 || c.Limits.QueueSize > 1000 || c.Limits.MaxFileSizeMB < 1 || c.Limits.MaxFileSizeMB > 1024 || c.Limits.MaxAudioDurationSeconds < 1 || c.Limits.MaxAudioDurationSeconds > 3600 || c.Limits.DownloadTimeoutSeconds < 1 || c.Limits.DownloadTimeoutSeconds > 600 {
		return errors.New("invalid limits: queue 1..1000, file MiB 1..1024, duration seconds 1..3600, download seconds 1..600")
	}
	for id, name := range c.Sounds {
		if !validID.MatchString(id) {
			return fmt.Errorf("invalid sound ID %q", id)
		}
		if name == "" || !filepath.IsLocal(name) {
			return fmt.Errorf("sound %q must be a relative path inside audio.directory", id)
		}
	}
	if c.MQTT.Enabled {
		u, err := url.Parse(c.MQTT.Broker)
		if err != nil || (u.Scheme != "tcp" && u.Scheme != "ssl") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" {
			return errors.New("mqtt.broker must be tcp://host:port or ssl://host:port; use username/password fields")
		}
		if c.MQTT.ClientID == "" || c.MQTT.Topic == "" {
			return errors.New("mqtt.client_id and mqtt.topic required")
		}
	}
	return nil
}

func (c Config) maxBytes() int64 { return c.Limits.MaxFileSizeMB * 1024 * 1024 }
func (c Config) drainTime() time.Duration {
	return time.Duration(c.Snapcast.BufferMS+c.Snapcast.SettleMS) * time.Millisecond
}
