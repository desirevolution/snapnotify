package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

//go:embed web/*
var webFS embed.FS

type apiError struct {
	Code    int
	Message string
}

func (e apiError) Error() string { return e.Message }
func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, err error) {
	code := 500
	message := "internal error; check service logs"
	var api apiError
	var tooLarge *http.MaxBytesError
	if errors.As(err, &api) {
		code, message = api.Code, api.Message
	} else if errors.As(err, &tooLarge) {
		code, message = 413, "file size limit exceeded"
	} else {
		slog.Error("request failed", "error", err)
	}
	writeJSON(w, code, map[string]string{"error": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return apiError{400, "invalid JSON request"}
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return apiError{400, "expected one JSON object"}
	}
	return nil
}

func (a *App) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		closing := a.closing
		a.mu.Unlock()
		if closing {
			fail(w, apiError{503, "stopping"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok", "version": version})
	})
	m.HandleFunc("DELETE /api/history", func(w http.ResponseWriter, r *http.Request) {
		a.clearHistory()
		writeJSON(w, 200, map[string]string{"status": "cleared"})
	})
	m.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.snapshot()) })
	m.HandleFunc("GET /api/sounds", func(w http.ResponseWriter, r *http.Request) {
		sounds, err := a.sounds()
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, sounds)
	})
	m.HandleFunc("GET /api/ids", func(w http.ResponseWriter, r *http.Request) {
		sounds, err := a.sounds()
		if err != nil {
			fail(w, err)
			return
		}
		ids := make([]string, 0, len(sounds))
		for _, sound := range sounds {
			ids = append(ids, sound.ID)
		}
		writeJSON(w, 200, ids)
	})
	m.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"max_file_size_mb": a.cfg.Limits.MaxFileSizeMB, "max_audio_duration_seconds": a.cfg.Limits.MaxAudioDurationSeconds, "queue_size": a.cfg.Limits.QueueSize, "volume": a.notificationVolume(), "version": version, "scan_directory": a.cfg.Audio.ScanDirectory})
	})
	m.HandleFunc("GET /api/volume", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]int{"volume": a.notificationVolume(), "default_volume": a.cfg.Audio.Volume})
	})
	m.HandleFunc("PUT /api/volume", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Volume *int `json:"volume"`
		}
		if err := readJSON(w, r, &payload); err != nil {
			fail(w, err)
			return
		}
		if payload.Volume == nil {
			fail(w, apiError{400, "volume is required"})
			return
		}
		if err := a.changeVolume(*payload.Volume); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"volume": *payload.Volume, "applies": "next_notification", "persistent": false})
	})
	accepted := func(w http.ResponseWriter, r *http.Request, job Job, err error) {
		if err == nil {
			err = r.Context().Err()
		}
		if err == nil {
			err = a.enqueuePrepared(&job)
		}
		if err != nil {
			job.cleanup()
			fail(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"status": "queued", "job": job})
	}
	m.HandleFunc("POST /play/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.acquire(); err != nil {
			fail(w, err)
			return
		}
		defer a.release()
		job, err := a.local(r.Context(), r.PathValue("id"))
		accepted(w, r, job, err)
	})
	m.HandleFunc("POST /play-url", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			URL  string `json:"url"`
			Save bool   `json:"save"`
		}
		if err := readJSON(w, r, &payload); err != nil {
			fail(w, err)
			return
		}
		if err := a.acquire(); err != nil {
			fail(w, err)
			return
		}
		defer a.release()
		if payload.Save && !a.cfg.Audio.ScanDirectory {
			fail(w, apiError{400, "saving requires audio.scan_directory: true"})
			return
		}
		job, err := a.fromURL(r.Context(), payload.URL, payload.Save)
		accepted(w, r, job, err)
	})
	m.HandleFunc("POST /play-file", func(w http.ResponseWriter, r *http.Request) {
		if err := a.acquire(); err != nil {
			fail(w, err)
			return
		}
		defer a.release()
		r.Body = http.MaxBytesReader(w, r.Body, a.cfg.maxBytes()+(64<<10))
		reader, err := r.MultipartReader()
		if err != nil {
			fail(w, apiError{400, "expected multipart form with file field"})
			return
		}
		var job Job
		seen := false
		save, saveSeen := false, false
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				err = e
				break
			}
			switch part.FormName() {
			case "file":
				if seen {
					err = apiError{400, "send exactly one file field"}
					part.Close()
					break
				}
				seen = true
				// Keep the source until all fields are read: save may follow file.
				job, err = a.prepareInput(r.Context(), part, filepath.Base(part.FileName()), true)
			case "save":
				if saveSeen || part.FileName() != "" {
					err = apiError{400, "send at most one boolean save field"}
					part.Close()
					break
				}
				saveSeen = true
				value, e := io.ReadAll(io.LimitReader(part, 17))
				if e != nil {
					err = e
					break
				}
				if string(value) != "true" && string(value) != "false" {
					err = apiError{400, "save must be true or false"}
					break
				}
				save = string(value) == "true"
			default:
				err = apiError{400, "expected file and optional save fields"}
			}
			part.Close()
			if err != nil {
				break
			}
		}

		if !seen && err == nil {
			err = apiError{400, "file field missing"}
		}
		if err == nil && save && !a.cfg.Audio.ScanDirectory {
			err = apiError{400, "saving requires audio.scan_directory: true"}
		}
		if err == nil && !save {
			os.Remove(job.originalPath)
			job.originalPath = ""
		}
		accepted(w, r, job, err)
	})
	assets, _ := fs.Sub(webFS, "web")
	m.Handle("GET /", http.FileServer(http.FS(assets)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		// No CORS. Reject browser cross-site submissions, including simple forms.
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, apiError{403, "cross-site requests rejected"})
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) || (u.Scheme != "http" && u.Scheme != "https") {
					fail(w, apiError{403, "origin rejected"})
					return
				}
			}
		}
		m.ServeHTTP(w, r)
	})
}
