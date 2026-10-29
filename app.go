package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Event struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	State string    `json:"state"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}
type Status struct {
	State     string  `json:"state"`
	Current   *Job    `json:"current"`
	Queue     int     `json:"queue"`
	LastError string  `json:"last_error,omitempty"`
	History   []Event `json:"history"`
	MQTT      string  `json:"mqtt"`
	Volume    int     `json:"volume"`
}
type App struct {
	cfg       Config
	ctx       context.Context
	tempDir   string
	jobs      chan Job
	preparing chan struct{}
	prepareWG sync.WaitGroup
	mu        sync.Mutex
	status    Status
	closing   bool
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func newApp(ctx context.Context, c Config, tempDir string) *App {
	mqttState := "disabled"
	if c.MQTT.Enabled {
		mqttState = "connecting"
	}
	a := &App{cfg: c, ctx: ctx, tempDir: tempDir, jobs: make(chan Job, c.Limits.QueueSize), preparing: make(chan struct{}, 2), status: Status{State: "idle", MQTT: mqttState, Volume: c.Audio.Volume, History: []Event{}}}
	return a
}
func (a *App) notificationVolume() int { a.mu.Lock(); defer a.mu.Unlock(); return a.status.Volume }
func (a *App) changeVolume(volume int) error {
	if volume < 0 || volume > 100 {
		return apiError{400, "volume must be an integer from 0 to 100"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return apiError{503, "service is stopping"}
	}
	a.status.Volume = volume
	return nil
}
func (a *App) snapshot() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.status
	s.Queue = len(a.jobs)
	s.History = append([]Event{}, s.History...)
	return s
}
func (a *App) clearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status.History = []Event{}
	a.status.LastError = ""
}
func (a *App) state(state string, job *Job) {
	a.mu.Lock()
	a.status.State = state
	a.status.Current = nil
	if job != nil {
		copy := *job
		a.status.Current = &copy
	}
	a.mu.Unlock()
}
func (a *App) event(job Job, state string, err error) {
	e := Event{ID: job.ID, Label: job.Label, State: state, At: time.Now()}
	a.mu.Lock()
	if err != nil {
		e.Error = err.Error()
		a.status.LastError = e.Error
	}
	a.status.History = append([]Event{e}, a.status.History...)
	if len(a.status.History) > 20 {
		a.status.History = a.status.History[:20]
	}
	a.mu.Unlock()
	if err != nil {
		slog.Error("notification failed", "job", job.ID, "label", job.Label, "error", err)
	} else {
		slog.Info("notification finished", "job", job.ID, "label", job.Label)
	}
}
func (a *App) enqueue(job Job) error {
	return a.enqueuePrepared(&job)
}

func (a *App) enqueuePrepared(job *Job) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || a.ctx.Err() != nil {
		return apiError{503, "service is stopping"}
	}
	if len(a.jobs) >= cap(a.jobs) {
		return apiError{429, "notification queue is full"}
	}
	if err := a.saveOriginal(job); err != nil {
		return err
	}
	select {
	case a.jobs <- *job:
		return nil
	default:
		return apiError{429, "notification queue is full"}
	}
}
func (a *App) acquire() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing || a.ctx.Err() != nil {
		return apiError{503, "service is stopping"}
	}
	select {
	case a.preparing <- struct{}{}:
		a.prepareWG.Add(1)
		return nil
	default:
		return apiError{503, "two files are already being prepared; retry shortly"}
	}
}
func (a *App) release() { <-a.preparing; a.prepareWG.Done() }

func (a *App) run(done chan<- struct{}) {
	defer close(done)
	defer func() {
		for {
			select {
			case job := <-a.jobs:
				os.Remove(job.Path)
			default:
				return
			}
		}
	}()
	for {
		var job Job
		select {
		case <-a.ctx.Done():
			return
		case job = <-a.jobs:
		}
		if a.ctx.Err() != nil {
			os.Remove(job.Path)
			return
		}
		a.state("starting", &job)
		s, err := a.begin(a.ctx)
		if err != nil {
			err = errors.Join(err, s.restore())
			os.Remove(job.Path)
			a.event(job, "failed", err)
			a.state("idle", nil)
			continue
		}
		for {
			a.state("playing", &job)
			err = s.updateVolume(a.ctx)
			if err == nil {
				err = s.play(a.ctx, job)
			}
			os.Remove(job.Path)
			if err != nil {
				err = errors.Join(err, s.restore())
				a.event(job, "failed", err)
				break
			}
			select {
			case next := <-a.jobs:
				a.event(job, "completed", nil)
				job = next
				if a.ctx.Err() == nil {
					continue
				}
				os.Remove(job.Path)
				err = errors.Join(a.ctx.Err(), s.restore())
				a.event(job, "failed", err)
			default:
				a.state("restoring", &job)
				err = s.silence(a.ctx, a.cfg.drainTime())
				err = errors.Join(err, s.restore())
				state := "completed"
				if err != nil {
					state = "failed"
				}
				a.event(job, state, err)
			}
			break
		}
		a.state("idle", nil)
	}
}
