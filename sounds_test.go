package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDirectoryDiscoveryAndExplicitMappings(t *testing.T) {
	a := apiApp(t)
	for _, name := range []string{"Bell.MP3", "My Sound!.wav", "gong.mp3", ".hidden.wav", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(a.cfg.Audio.Directory, name), wave(48000, 1, 100), 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.Mkdir(filepath.Join(a.cfg.Audio.Directory, "nested"), 0700)
	os.WriteFile(filepath.Join(a.cfg.Audio.Directory, "nested", "child.wav"), wave(48000, 1, 100), 0600)
	os.Symlink(filepath.Join(a.cfg.Audio.Directory, "gong.wav"), filepath.Join(a.cfg.Audio.Directory, "link.wav"))
	sounds, err := a.sounds()
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]Sound{}
	for _, s := range sounds {
		byFile[s.Filename] = s
		if !validID.MatchString(s.ID) {
			t.Fatalf("invalid generated ID %q", s.ID)
		}
	}
	if len(sounds) != 4 || byFile["gong.wav"].ID != "gong" || byFile["gong.wav"].Source != "mapping" || byFile["Bell.MP3"].ID != "bell" || byFile["My Sound!.wav"].ID != "my_sound" || byFile["gong.mp3"].ID == "gong" {
		t.Fatalf("wrong catalog: %+v", sounds)
	}
	w := call(a, "GET", "/api/ids", "", "")
	var ids []string
	if err = json.Unmarshal(w.Body.Bytes(), &ids); err != nil {
		t.Fatal(err)
	}
	w = call(a, "GET", "/api/sounds", "", "")
	var listed []Sound
	json.Unmarshal(w.Body.Bytes(), &listed)
	if len(ids) != len(sounds) || len(listed) != len(sounds) {
		t.Fatal("ID endpoint and full catalog differ")
	}
	job, err := a.local(context.Background(), "my_sound")
	if err != nil {
		t.Fatal(err)
	}
	job.cleanup()
	os.Remove(filepath.Join(a.cfg.Audio.Directory, "My Sound!.wav"))
	if w = call(a, "POST", "/play/my_sound", "", ""); w.Code != 404 {
		t.Fatal("deleted discovered file still available")
	}
	a.cfg.Audio.ScanDirectory = false
	sounds, err = a.sounds()
	if err != nil || len(sounds) != 1 || sounds[0].ID != "gong" {
		t.Fatal("scan disabling failed")
	}
}

func TestDiscoveryConflictsAreStable(t *testing.T) {
	a := apiApp(t)
	for _, name := range []string{"Bell.wav", "bell.mp3", strings.Repeat("a", 80) + ".wav", "🎵.ogg"} {
		os.WriteFile(filepath.Join(a.cfg.Audio.Directory, name), []byte("metadata only"), 0600)
	}
	one, err := a.sounds()
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.sounds()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) {
		t.Fatal("catalog IDs are not stable")
	}
	for _, s := range one {
		if s.Filename == "Bell.wav" || s.Filename == "bell.mp3" {
			if s.ID == "bell" || !strings.HasPrefix(s.ID, "bell-") {
				t.Fatal("ambiguous bare ID assigned")
			}
		}
		if !validID.MatchString(s.ID) {
			t.Fatalf("invalid ID: %s", s.ID)
		}
	}
}

func TestMissingDirectoryStillListsExplicitIDs(t *testing.T) {
	a := apiApp(t)
	a.cfg.Audio.Directory = filepath.Join(t.TempDir(), "missing")
	w := call(a, "GET", "/api/ids", "", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[\"gong\"]" {
		t.Fatalf("missing directory: %d %s", w.Code, w.Body)
	}
}
