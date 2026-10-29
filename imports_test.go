package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploaded(t *testing.T, name string, data []byte) (string, string) {
	t.Helper()
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	writer.WriteField("save", "true")
	writer.Close()
	return b.String(), writer.FormDataContentType()
}
func savedReply(t *testing.T, w *httptest.ResponseRecorder) Job {
	t.Helper()
	if w.Code != 202 {
		t.Fatalf("expected queued import: %d %s", w.Code, w.Body)
	}
	var reply struct {
		Job Job `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Job.SavedSound == nil {
		t.Fatal("saved sound ID missing")
	}
	return reply.Job
}
func drainJob(a *App) {
	select {
	case j := <-a.jobs:
		j.cleanup()
	default:
	}
}

func TestUploadSavingDiscoveryDeduplicationAndRestart(t *testing.T) {
	a := apiApp(t)
	data := wave(48000, 1, 200)
	body, ct := uploaded(t, "gong.wav", data)
	first := savedReply(t, call(a, "POST", "/play-file", body, ct))
	drainJob(a)
	saved := first.SavedSound
	if !strings.HasPrefix(saved.ID, "gong-") || saved.Filename == "gong.wav" {
		t.Fatal("import would overwrite existing sound")
	}
	got, err := os.ReadFile(filepath.Join(a.cfg.Audio.Directory, saved.Filename))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("original encoded audio was not saved")
	}
	if len(a.cfg.Sounds) != 1 || a.cfg.Sounds["gong"] != "gong.wav" {
		t.Fatal("manual mappings changed")
	}
	second := savedReply(t, call(a, "POST", "/play-file", body, ct))
	drainJob(a)
	if first.SavedSound.ID != second.SavedSound.ID {
		t.Fatal("same file/name was not deduplicated")
	}
	files, _ := os.ReadDir(a.cfg.Audio.Directory)
	if len(files) != 2 {
		t.Fatalf("unexpected saved files: %d", len(files))
	}
	restarted := newApp(context.Background(), a.cfg, t.TempDir())
	job, err := restarted.local(context.Background(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.cleanup()
	if w := call(restarted, "GET", "/api/ids", "", ""); !strings.Contains(w.Body.String(), saved.ID) {
		t.Fatal("saved ID missing after restart")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 0 {
		t.Fatal("import temporary files leaked")
	}
}

func TestRejectedImportsAreNotSaved(t *testing.T) {
	a := apiApp(t)
	bad, ct := uploaded(t, "bad.wav", []byte("invalid"))
	if w := call(a, "POST", "/play-file", bad, ct); w.Code != 422 {
		t.Fatal("invalid upload accepted")
	}
	// A second multipart field invalidates the whole request before storage.
	body, ct := uploadBody(t, 2)
	if w := call(a, "POST", "/play-file", body, ct); w.Code != 400 {
		t.Fatal("extra fields accepted")
	}
	if w := call(a, "POST", "/play/gong", "", ""); w.Code != 202 {
		t.Fatal(w.Body)
	}
	body, ct = uploaded(t, "new.wav", wave(48000, 1, 100))
	if w := call(a, "POST", "/play-file", body, ct); w.Code != 429 {
		t.Fatal("full queue accepted import")
	}
	drainJob(a)
	files, _ := os.ReadDir(a.cfg.Audio.Directory)
	if len(files) != 1 {
		t.Fatal("rejected request saved a file")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 0 {
		t.Fatal("rejected request leaked temporary files")
	}
}

func TestURLSavingUsesDetectedFormat(t *testing.T) {
	a := apiApp(t)
	data := wave(48000, 1, 200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	body, _ := json.Marshal(map[string]any{"url": server.URL + "/notification.mp3?token=secret", "save": true})
	j := savedReply(t, call(a, "POST", "/play-url", string(body), "application/json"))
	drainJob(a)
	if filepath.Ext(j.SavedSound.Filename) != ".wav" || strings.Contains(j.SavedSound.Filename, "secret") {
		t.Fatal("saved filename did not use detected format")
	}
	body, _ = json.Marshal(map[string]any{"url": server.URL + "/notification.mp3?token=secret", "save": false})
	w := call(a, "POST", "/play-url", string(body), "application/json")
	if w.Code != 202 || strings.Contains(w.Body.String(), "saved_sound") {
		t.Fatal("disabled URL saving still active")
	}
	drainJob(a)
	files, _ := os.ReadDir(a.cfg.Audio.Directory)
	if len(files) != 2 {
		t.Fatal("disabled saving persisted another file")
	}
}

func TestSavingFailureDoesNotQueue(t *testing.T) {
	a := apiApp(t)
	// Use a regular file as the destination to reliably test failure as root.
	a.cfg.Audio.Directory = filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(a.cfg.Audio.Directory, []byte("x"), 0600)
	body, ct := uploaded(t, "new.wav", wave(48000, 1, 100))
	w := call(a, "POST", "/play-file", body, ct)
	if w.Code != 500 || len(a.jobs) != 0 {
		t.Fatal("failed saving queued audio")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 0 {
		t.Fatal("failed saving leaked temporary files")
	}
}

func TestSavingRequiresDiscovery(t *testing.T) {
	a := apiApp(t)
	a.cfg.Audio.ScanDirectory = false
	body, ct := uploaded(t, "new.wav", wave(48000, 1, 100))
	if w := call(a, "POST", "/play-file", body, ct); w.Code != 400 {
		t.Fatal("saving without discovery accepted")
	}
	if len(a.jobs) != 0 {
		t.Fatal("invalid save request queued")
	}
}

func TestStoredFilenameCollisionNeverOverwrites(t *testing.T) {
	a := apiApp(t)
	data := wave(48000, 1, 100)
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	name := "new-" + digest[:12] + ".wav"
	path := filepath.Join(a.cfg.Audio.Directory, name)
	if err := os.WriteFile(path, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	body, ct := uploaded(t, "new.wav", data)
	j := savedReply(t, call(a, "POST", "/play-file", body, ct))
	drainJob(a)
	if j.SavedSound.Filename == name {
		t.Fatal("colliding file was reused")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "keep this file" {
		t.Fatal("existing file overwritten")
	}
}

func TestManualCatalogCollisionStillReturnsUsableSavedID(t *testing.T) {
	a := apiApp(t)
	data := wave(48000, 1, 100)
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	a.cfg.Sounds["new-"+digest[:12]] = "gong.wav"
	body, ct := uploaded(t, "new.wav", data)
	j := savedReply(t, call(a, "POST", "/play-file", body, ct))
	drainJob(a)
	if j.SavedSound.ID == "new-"+digest[:12] {
		t.Fatal("manual ID replaced")
	}
	played, err := a.local(context.Background(), j.SavedSound.ID)
	if err != nil {
		t.Fatal(err)
	}
	played.cleanup()
}

func TestUploadSaveIsPerRequestAndOrderIndependent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		before    bool
		wantSaved bool
	}{{"omitted", "", false, false}, {"false-before", "false", true, false}, {"false-after", "false", false, false}, {"true-before", "true", true, true}, {"true-after", "true", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			a := apiApp(t)
			var b bytes.Buffer
			w := multipart.NewWriter(&b)
			if tc.before {
				w.WriteField("save", tc.value)
			}
			part, _ := w.CreateFormFile("file", "request.wav")
			part.Write(wave(48000, 1, 100))
			if !tc.before && tc.value != "" {
				w.WriteField("save", tc.value)
			}
			w.Close()
			reply := call(a, "POST", "/play-file", b.String(), w.FormDataContentType())
			if reply.Code != 202 {
				t.Fatalf("%d %s", reply.Code, reply.Body)
			}
			if strings.Contains(reply.Body.String(), "saved_sound") != tc.wantSaved {
				t.Fatal("wrong per-request save result")
			}
			drainJob(a)
			files, _ := os.ReadDir(a.cfg.Audio.Directory)
			expected := 1
			if tc.wantSaved {
				expected = 2
			}
			if len(files) != expected {
				t.Fatal("wrong stored files")
			}
			entries, _ := os.ReadDir(a.tempDir)
			if len(entries) != 0 {
				t.Fatal("per-request import leaked temporary files")
			}
		})
	}
}

func TestInvalidSaveFieldsAreRejected(t *testing.T) {
	for _, value := range []string{"yes", "1", "TRUE", "true ", ""} {
		a := apiApp(t)
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		part, _ := w.CreateFormFile("file", "request.wav")
		part.Write(wave(48000, 1, 100))
		w.WriteField("save", value)
		w.Close()
		if reply := call(a, "POST", "/play-file", b.String(), w.FormDataContentType()); reply.Code != 400 {
			t.Fatalf("invalid save value accepted: %q", value)
		}
		if len(a.jobs) != 0 {
			t.Fatal("invalid request queued")
		}
		files, _ := os.ReadDir(a.cfg.Audio.Directory)
		if len(files) != 1 {
			t.Fatal("invalid request saved")
		}
		entries, _ := os.ReadDir(a.tempDir)
		if len(entries) != 0 {
			t.Fatal("invalid save field leaked source")
		}
	}
}

func TestURLSaveDoesNotCarryOverToNextRequest(t *testing.T) {
	a := apiApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(wave(48000, 1, 100)) }))
	defer server.Close()
	for i, payload := range []map[string]any{{"url": server.URL + "/saved.wav", "save": true}, {"url": server.URL + "/temporary.wav"}, {"url": server.URL + "/other.wav", "save": false}} {
		body, _ := json.Marshal(payload)
		reply := call(a, "POST", "/play-url", string(body), "application/json")
		if reply.Code != 202 {
			t.Fatal(reply.Body)
		}
		if strings.Contains(reply.Body.String(), "saved_sound") != (i == 0) {
			t.Fatal("save selection carried over")
		}
		drainJob(a)
	}
	files, _ := os.ReadDir(a.cfg.Audio.Directory)
	if len(files) != 2 {
		t.Fatal("temporary URL request persisted a file")
	}
}
