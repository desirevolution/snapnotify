package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type testRig struct {
	mu                  sync.Mutex
	status              ServerStatus
	mpdState            string
	pauseCalls          int
	resumeCalls         int
	streamCalls         int
	failSwitch          string
	control, audio, mpd net.Listener
	audioResult         chan []byte
}

func listenTest(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
func rig(t *testing.T) (*testRig, *App) {
	t.Helper()
	r := &testRig{mpdState: "play", control: listenTest(t), audio: listenTest(t), mpd: listenTest(t), audioResult: make(chan []byte, 10)}
	var c1, c2 Client
	c1.ID = "audible"
	c1.Connected = true
	c1.Config.Volume = Volume{Percent: 25}
	c2.ID = "muted"
	c2.Connected = true
	c2.Config.Volume = Volume{Percent: 40, Muted: true}
	r.status.Server.Groups = []Group{{ID: "g", StreamID: "Mixed", Clients: []Client{c1, c2}}}
	r.status.Server.Streams = []struct {
		ID string `json:"id"`
	}{{"c1"}, {"Mixed"}}
	go r.acceptRPC()
	go r.acceptMPD()
	go func() {
		for {
			c, err := r.audio.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); data, _ := io.ReadAll(c); r.audioResult <- data }()
		}
	}()
	c := defaultConfig()
	c.Snapcast.Stream = "c1"
	c.Snapcast.ControlAddress = r.control.Addr().String()
	c.Snapcast.AudioAddress = r.audio.Addr().String()
	c.MPD.Address = r.mpd.Addr().String()
	c.Snapcast.BufferMS = 0
	c.Snapcast.SettleMS = 0
	return r, newApp(context.Background(), c, t.TempDir())
}
func (r *testRig) acceptRPC() {
	for {
		c, err := r.control.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
				Params struct {
					ID       string `json:"id"`
					StreamID string `json:"stream_id"`
					Volume   Volume `json:"volume"`
				} `json:"params"`
			}
			if json.NewDecoder(c).Decode(&req) != nil {
				return
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			var result any = map[string]any{}
			var rpcError any
			switch req.Method {
			case "Server.GetStatus":
				result = r.status
			case "Group.SetStream":
				r.streamCalls++
				if req.Params.ID == r.failSwitch && req.Params.StreamID == "c1" {
					rpcError = map[string]any{"code": -1, "message": "test failure"}
				} else {
					for i := range r.status.Server.Groups {
						if r.status.Server.Groups[i].ID == req.Params.ID {
							r.status.Server.Groups[i].StreamID = req.Params.StreamID
						}
					}
				}
			case "Client.SetVolume":
				for i := range r.status.Server.Groups {
					for j := range r.status.Server.Groups[i].Clients {
						if r.status.Server.Groups[i].Clients[j].ID == req.Params.ID {
							r.status.Server.Groups[i].Clients[j].Config.Volume = req.Params.Volume
						}
					}
				}
			default:
				rpcError = map[string]any{"code": -32601, "message": "unknown method"}
			}
			// Exercise skipping unsolicited notifications before the response.
			fmt.Fprintln(c, `{"jsonrpc":"2.0","method":"Server.OnUpdate"}`)
			json.NewEncoder(c).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result, "error": rpcError})
		}()
	}
}
func (r *testRig) acceptMPD() {
	for {
		c, err := r.mpd.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			fmt.Fprintln(c, "OK MPD 0.24.0")
			scanner := bufio.NewScanner(c)
			for scanner.Scan() {
				line := scanner.Text()
				r.mu.Lock()
				switch line {
				case "status":
					fmt.Fprintf(c, "state: %s\nsongid: 42\nelapsed: 1.000\nOK\n", r.mpdState)
				case "pause 1":
					r.mpdState = "pause"
					r.pauseCalls++
					fmt.Fprintln(c, "OK")
				case "pause 0":
					r.mpdState = "play"
					r.resumeCalls++
					fmt.Fprintln(c, "OK")
				default:
					fmt.Fprintln(c, "ACK [5@0] {} unknown command")
				}
				r.mu.Unlock()
			}
		}()
	}
}

func TestSessionRestoresAndRespectsMute(t *testing.T) {
	r, a := rig(t)
	s, err := a.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	if r.mpdState != "pause" || r.status.Server.Groups[0].StreamID != "c1" || r.status.Server.Groups[0].Clients[0].Config.Volume.Percent != 60 || !r.status.Server.Groups[0].Clients[1].Config.Volume.Muted {
		t.Fatal("incorrect notification state")
	}
	r.mu.Unlock()
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mpdState != "play" || r.status.Server.Groups[0].StreamID != "Mixed" || r.status.Server.Groups[0].Clients[0].Config.Volume.Percent != 25 || r.status.Server.Groups[0].Clients[1].Config.Volume != (Volume{Percent: 40, Muted: true}) {
		t.Fatal("original state was not restored")
	}
}
func TestExternalChangesArePreserved(t *testing.T) {
	r, a := rig(t)
	s, err := a.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.status.Server.Groups[0].Clients[0].Config.Volume = Volume{Percent: 33, Muted: true}
	r.status.Server.Groups[0].StreamID = "other"
	r.mpdState = "stop"
	r.mu.Unlock()
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Server.Groups[0].Clients[0].Config.Volume != (Volume{Percent: 33, Muted: true}) || r.status.Server.Groups[0].StreamID != "other" || r.mpdState != "stop" {
		t.Fatal("manual changes overwritten")
	}
}

func TestRuntimeVolumeAndRestore(t *testing.T) {
	r, a := rig(t)
	a.changeVolume(75)
	s, err := a.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	v := r.status.Server.Groups[0].Clients[0].Config.Volume.Percent
	r.mu.Unlock()
	if v != 75 {
		t.Fatalf("want runtime volume 75, got %d", v)
	}
	a.changeVolume(42)
	if err = s.updateVolume(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	v = r.status.Server.Groups[0].Clients[0].Config.Volume.Percent
	r.mu.Unlock()
	if v != 42 {
		t.Fatal("next notification did not use updated volume")
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.Server.Groups[0].Clients[0].Config.Volume.Percent != 25 {
		t.Fatal("runtime volume override prevented restoration")
	}
}
func TestPartialFailureRestoresMPDAndStreams(t *testing.T) {
	r, a := rig(t)
	r.mu.Lock()
	g := r.status.Server.Groups[0]
	g.ID = "bad"
	g.Clients = nil
	var c Client
	c.ID = "other"
	c.Connected = true
	g.Clients = []Client{c}
	r.status.Server.Groups = append(r.status.Server.Groups, g)
	r.failSwitch = "bad"
	r.mu.Unlock()
	s, err := a.begin(context.Background())
	if err == nil {
		t.Fatal("expected stream switch failure")
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mpdState != "play" || r.status.Server.Groups[0].StreamID != "Mixed" {
		t.Fatal("partial failure not restored")
	}
}
func TestNoAudibleClientsDoesNotPause(t *testing.T) {
	r, a := rig(t)
	r.mu.Lock()
	r.status.Server.Groups[0].Muted = true
	r.mu.Unlock()
	s, err := a.begin(context.Background())
	if err == nil {
		t.Fatal("expected no-client error")
	}
	s.restore()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pauseCalls != 0 {
		t.Fatal("MPD paused without listeners")
	}
}
func TestAlreadyPausedMPDStaysPaused(t *testing.T) {
	r, a := rig(t)
	r.mu.Lock()
	r.mpdState = "pause"
	r.mu.Unlock()
	s, err := a.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resumeCalls != 0 || r.mpdState != "pause" {
		t.Fatal("MPD unexpectedly resumed")
	}
}

func TestNoMPDConfiguredStillPlaysAndRestores(t *testing.T) {
	r, a := rig(t)
	a.cfg.MPD.Address = ""
	// Closing the listener makes any accidental MPD call fail immediately.
	r.mpd.Close()
	s, err := a.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.silence(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pauseCalls != 0 || r.resumeCalls != 0 || r.status.Server.Groups[0].StreamID != "Mixed" || r.status.Server.Groups[0].Clients[0].Config.Volume.Percent != 25 {
		t.Fatal("MPD-free session did not restore Snapcast")
	}
}

func TestConfiguredUnavailableMPDFailsBeforeSwitching(t *testing.T) {
	r, a := rig(t)
	r.mpd.Close()
	s, err := a.begin(context.Background())
	if err == nil {
		t.Fatal("explicitly configured unavailable MPD must report an error")
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streamCalls != 0 {
		t.Fatal("streams changed despite failed MPD check")
	}
}
func TestQueueFIFOAndPCMDelivery(t *testing.T) {
	r, a := rig(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	defer cancel()
	pcm1 := bytes.Repeat([]byte{1, 2, 3, 4}, 960)
	pcm2 := bytes.Repeat([]byte{5, 6, 7, 8}, 960)
	for i, data := range [][]byte{pcm1, pcm2} {
		f, err := os.CreateTemp(a.tempDir, "job-*")
		if err != nil {
			t.Fatal(err)
		}
		f.Write(data)
		f.Close()
		if err = a.enqueue(Job{ID: fmt.Sprint(i), Label: fmt.Sprint(i), Path: f.Name()}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go a.run(done)
	select {
	case got := <-r.audioResult:
		if !bytes.Equal(got, append(pcm1, pcm2...)) {
			t.Fatalf("PCM delivery differs: %d bytes", len(got))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker timed out")
	}
	cancel()
	<-done
	s := a.snapshot()
	if len(s.History) != 2 || s.History[0].ID != "1" || s.History[1].ID != "0" || s.LastError != "" {
		t.Fatalf("wrong history: %+v", s)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pauseCalls != 1 || r.resumeCalls != 1 {
		t.Fatal("burst should pause/resume only once")
	}
	entries, _ := os.ReadDir(a.tempDir)
	if len(entries) != 0 {
		t.Fatal("queued files leaked")
	}
}
func TestCancelledPlaybackRestores(t *testing.T) {
	r, a := rig(t)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := a.begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = s.silence(ctx, time.Second); err == nil {
		t.Fatal("expected cancellation")
	}
	if err = s.restore(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mpdState != "play" || r.status.Server.Groups[0].StreamID != "Mixed" {
		t.Fatal("cancel did not restore")
	}
}
func TestMPDErrorDoesNotLeakPassword(t *testing.T) {
	_, a := rig(t)
	a.cfg.MPD.Password = "secret"
	_, err := a.mpd(context.Background(), "status")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid auth error: %v", err)
	}
}
