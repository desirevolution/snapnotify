package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func apiApp(t *testing.T) *App {
	t.Helper()
	c := defaultConfig()
	c.Audio.Directory = t.TempDir()
	c.Sounds = map[string]string{"gong": "gong.wav"}
	c.Limits.QueueSize = 1
	c.Limits.MaxFileSizeMB = 1
	if err := os.WriteFile(filepath.Join(c.Audio.Directory, "gong.wav"), wave(48000, 1, 960), 0600); err != nil {
		t.Fatal(err)
	}
	a := newApp(context.Background(), c, t.TempDir())
	t.Cleanup(func() {
		for {
			select {
			case j := <-a.jobs:
				os.Remove(j.Path)
			default:
				return
			}
		}
	})
	return a
}

func TestClearHistoryPreservesPlaybackAndQueue(t *testing.T) {
	a := apiApp(t)
	if w := call(a, "POST", "/play/gong", "", ""); w.Code != 202 {
		t.Fatal(w.Body)
	}
	active := Job{ID: "playing", Label: "gong"}
	a.state("playing", &active)
	a.event(Job{ID: "old", Label: "old"}, "failed", errors.New("old failure"))
	w := call(a, "DELETE", "/api/history", "", "")
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	s := a.snapshot()
	if len(s.History) != 0 || s.LastError != "" || s.Queue != 1 || s.Current.ID != "playing" || s.State != "playing" {
		t.Fatalf("history clear affected playback: %+v", s)
	}
	a.event(Job{ID: "new", Label: "new"}, "completed", nil)
	if a.snapshot().History[0].ID != "new" {
		t.Fatal("new events lost after clear")
	}
	request := httptest.NewRequest("DELETE", "/api/history", nil)
	request.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	a.handler().ServeHTTP(rec, request)
	if rec.Code != 403 || len(a.snapshot().History) != 1 {
		t.Fatal("cross-site history deletion allowed")
	}
}
func call(a *App, method, path, body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, r)
	return w
}
func TestHTTPIDsQueueAndStatus(t *testing.T) {
	a := apiApp(t)
	if w := call(a, "POST", "/play/unknown", "", ""); w.Code != 404 {
		t.Fatalf("unknown ID: %d %s", w.Code, w.Body)
	}
	if w := call(a, "POST", "/play/gong", "", ""); w.Code != 202 {
		t.Fatalf("enqueue: %d %s", w.Code, w.Body)
	}
	if w := call(a, "POST", "/play/gong", "", ""); w.Code != 429 {
		t.Fatalf("queue limit: %d %s", w.Code, w.Body)
	}
	w := call(a, "GET", "/api/status", "", "")
	var status Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Queue != 1 {
		t.Fatal("missing queued job")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 1 {
		t.Fatalf("queue rejection leaked files: %d", len(entries))
	}
}
func TestURLDownloadAndRestrictions(t *testing.T) {
	a := apiApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(wave(44100, 1, 441)) }))
	defer server.Close()
	body, _ := json.Marshal(map[string]string{"url": server.URL + "/tone?token=secret"})
	w := call(a, "POST", "/play-url", string(body), "application/json")
	if w.Code != 202 {
		t.Fatalf("URL: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("URL credentials leaked into label")
	}
	for _, body := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"https://user:secret@example.org/a.mp3"}`, `{"url":"ftp://example.org/a"}`, `{"url":"http://a/","extra":true}`, `{} {}`} {
		if w = call(a, "POST", "/play-url", body, "application/json"); w.Code != 400 {
			t.Fatalf("bad URL request: %d %s", w.Code, w.Body)
		}
	}
}
func uploadBody(t *testing.T, files int) (string, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for i := 0; i < files; i++ {
		part, err := w.CreateFormFile("file", "tone.wav")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(wave(48000, 1, 100))
	}
	w.Close()
	return body.String(), w.FormDataContentType()
}
func TestUploadAndCleanup(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		t.Run(string(rune('0'+n)), func(t *testing.T) {
			a := apiApp(t)
			body, ct := uploadBody(t, n)
			w := call(a, "POST", "/play-file", body, ct)
			want := 400
			if n == 1 {
				want = 202
			}
			if w.Code != want {
				t.Fatalf("upload: %d %s", w.Code, w.Body)
			}
			entries, _ := os.ReadDir(a.tempDir)
			wantFiles := 0
			if n == 1 {
				wantFiles = 1
			}
			if len(entries) != wantFiles {
				t.Fatal("upload temporary files leaked")
			}
		})
	}
}
func TestCrossSiteBlockedAndUIEmbedded(t *testing.T) {
	a := apiApp(t)
	for _, header := range []string{"Origin", "Sec-Fetch-Site"} {
		r := httptest.NewRequest("POST", "/play/gong", nil)
		value := "https://evil.example"
		if header == "Sec-Fetch-Site" {
			value = "cross-site"
		}
		r.Header.Set(header, value)
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("cross-site request accepted")
		}
	}
	for _, path := range []string{"/", "/app.js", "/style.css", "/healthz"} {
		w := call(a, "GET", path, "", "")
		if w.Code != 200 {
			t.Fatalf("missing embedded asset %s", path)
		}
	}
}
func TestPreparationConcurrencyLimit(t *testing.T) {
	a := apiApp(t)
	if a.acquire() != nil || a.acquire() != nil {
		t.Fatal("first two preparation slots unavailable")
	}
	if a.acquire() == nil {
		t.Fatal("unbounded preparation")
	}
	a.release()
	a.release()
}

func TestVolumeAPI(t *testing.T) {
	a := apiApp(t)
	for _, body := range []string{`{}`, `{"volume":null}`, `{"volume":-1}`, `{"volume":101}`, `{"volume":1.5}`, `{"volume":"60"}`} {
		if w := call(a, "PUT", "/api/volume", body, "application/json"); w.Code != 400 {
			t.Fatalf("invalid volume accepted: %s => %d", body, w.Code)
		}
	}
	for _, v := range []string{"0", "83", "100"} {
		w := call(a, "PUT", "/api/volume", `{"volume":`+v+`}`, "application/json")
		if w.Code != 200 {
			t.Fatalf("volume update: %s", w.Body)
		}
		w = call(a, "GET", "/api/volume", "", "")
		var body struct {
			Volume  int `json:"volume"`
			Default int `json:"default_volume"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		if body.Default != 60 || a.cfg.Audio.Volume != 60 {
			t.Fatal("runtime update changed config default")
		}
	}
	if a.notificationVolume() != 100 || a.snapshot().Volume != 100 {
		t.Fatal("status missing runtime volume")
	}
}
func TestDownloadErrorsAndRedirects(t *testing.T) {
	for _, handler := range []http.HandlerFunc{
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) },
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "file:///etc/passwd")
			w.WriteHeader(302)
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "2000000")
			w.WriteHeader(200)
		},
		func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "not audio") },
	} {
		a := apiApp(t)
		s := httptest.NewServer(handler)
		_, err := a.fromURL(context.Background(), s.URL, false)
		s.Close()
		if err == nil {
			t.Fatal("invalid download accepted")
		}
		entries, _ := os.ReadDir(a.tempDir)
		if len(entries) > 0 {
			t.Fatal("failed download leaked files")
		}
	}
}
