package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const rpcTimeout = 4 * time.Second

type Volume struct {
	Muted   bool `json:"muted"`
	Percent int  `json:"percent"`
}
type Client struct {
	ID        string `json:"id"`
	Connected bool   `json:"connected"`
	Config    struct {
		Volume Volume `json:"volume"`
	} `json:"config"`
}
type Group struct {
	ID       string   `json:"id"`
	StreamID string   `json:"stream_id"`
	Muted    bool     `json:"muted"`
	Clients  []Client `json:"clients"`
}
type ServerStatus struct {
	Server struct {
		Groups  []Group `json:"groups"`
		Streams []struct {
			ID string `json:"id"`
		} `json:"streams"`
	} `json:"server"`
}

func rpc(ctx context.Context, address, method string, params any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("snapcast connection: %w", err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", 1, method, params}
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 4<<20))
	for {
		var response struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = decoder.Decode(&response); err != nil {
			return err
		}
		if response.ID == nil || *response.ID != 1 {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%s: %s (%d)", method, response.Error.Message, response.Error.Code)
		}
		if result != nil {
			return json.Unmarshal(response.Result, result)
		}
		return nil
	}
}

func (a *App) snapStatus(ctx context.Context) (ServerStatus, error) {
	var s ServerStatus
	err := rpc(ctx, a.cfg.Snapcast.ControlAddress, "Server.GetStatus", nil, &s)
	return s, err
}
func (a *App) setStream(ctx context.Context, id, stream string) error {
	return rpc(ctx, a.cfg.Snapcast.ControlAddress, "Group.SetStream", map[string]any{"id": id, "stream_id": stream}, nil)
}
func (a *App) setVolume(ctx context.Context, id string, v Volume) error {
	return rpc(ctx, a.cfg.Snapcast.ControlAddress, "Client.SetVolume", map[string]any{"id": id, "volume": v}, nil)
}

func (a *App) mpd(ctx context.Context, command string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", a.cfg.MPD.Address)
	if err != nil {
		return nil, fmt.Errorf("MPD connection: %w", err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	r := bufio.NewReader(io.LimitReader(conn, 1<<20))
	greeting, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(greeting, "OK MPD ") {
		return nil, errors.New("invalid MPD greeting")
	}
	run := func(cmd string) (map[string]string, error) {
		if _, err := fmt.Fprintln(conn, cmd); err != nil {
			return nil, err
		}
		values := map[string]string{}
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimSuffix(line, "\n")
			if line == "OK" {
				return values, nil
			}
			if strings.HasPrefix(line, "ACK ") {
				return nil, errors.New("MPD rejected command: " + strings.Split(cmd, " ")[0])
			}
			k, v, ok := strings.Cut(line, ": ")
			if ok {
				values[k] = v
			}
		}
	}
	if a.cfg.MPD.Password != "" {
		if _, err = run("password " + strconv.Quote(a.cfg.MPD.Password)); err != nil {
			return nil, err
		}
	}
	return run(command)
}

type session struct {
	a          *App
	groups     map[string]string
	volumes    map[string]Volume
	applied    map[string]Volume
	volume     int
	paused     bool
	mpdSong    string
	mpdElapsed string
	conn       net.Conn
	stopConn   func() bool
	nextWrite  time.Time
}

func (a *App) begin(ctx context.Context) (s *session, err error) {
	s = &session{a: a, groups: map[string]string{}, volumes: map[string]Volume{}, applied: map[string]Volume{}, volume: a.notificationVolume()}
	// Always return the partial session so callers can undo partially applied changes.
	status, err := a.snapStatus(ctx)
	if err != nil {
		return s, err
	}
	found := false
	for _, st := range status.Server.Streams {
		if st.ID == a.cfg.Snapcast.Stream {
			found = true
		}
	}
	if !found {
		return s, errors.New("notification stream not found in Snapcast")
	}
	var groups []Group
	for _, g := range status.Server.Groups {
		if g.Muted {
			continue
		}
		for _, c := range g.Clients {
			if c.Connected && !c.Config.Volume.Muted {
				groups = append(groups, g)
				break
			}
		}
	}
	if len(groups) == 0 {
		return s, errors.New("no connected, unmuted Snapcast clients")
	}
	if a.cfg.MPD.Address != "" {
		state, err := a.mpd(ctx, "status")
		if err != nil {
			return s, err
		}
		if state["state"] == "play" {
			// Mark before sending: an acknowledgement may be lost after MPD paused.
			s.paused = true
			s.mpdSong = state["songid"]
			if _, err = a.mpd(ctx, "pause 1"); err != nil {
				return s, err
			}
			paused, err := a.mpd(ctx, "status")
			if err != nil {
				return s, err
			}
			s.mpdElapsed = paused["elapsed"]
		}
	}
	for _, g := range groups {
		if g.StreamID != a.cfg.Snapcast.Stream {
			s.groups[g.ID] = g.StreamID
			if err = a.setStream(ctx, g.ID, a.cfg.Snapcast.Stream); err != nil {
				return s, err
			}
		}
	}
	s.conn, err = (&net.Dialer{Timeout: rpcTimeout}).DialContext(ctx, "tcp", a.cfg.Snapcast.AudioAddress)
	if err != nil {
		return s, fmt.Errorf("notification audio connection: %w", err)
	}
	s.stopConn = context.AfterFunc(ctx, func() { s.conn.Close() })
	// Flush old buffered music before increasing volume. Silence also gives
	// clients time to switch to the new stream before the first audible sample.
	if err = s.silence(ctx, a.cfg.drainTime()); err != nil {
		return s, err
	}
	for _, g := range groups {
		for _, c := range g.Clients {
			if !c.Connected || c.Config.Volume.Muted {
				continue
			}
			s.volumes[c.ID] = c.Config.Volume
			s.applied[c.ID] = Volume{Percent: s.volume}
			if err = a.setVolume(ctx, c.ID, s.applied[c.ID]); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

func (s *session) updateVolume(ctx context.Context) error {
	volume := s.a.notificationVolume()
	if volume == s.volume {
		return nil
	}
	// Drain the previous notification before changing client volume.
	if err := s.silence(ctx, s.a.cfg.drainTime()); err != nil {
		return err
	}
	status, err := s.a.snapStatus(ctx)
	if err != nil {
		return err
	}
	for _, g := range status.Server.Groups {
		for _, c := range g.Clients {
			if last, ok := s.applied[c.ID]; ok && c.Config.Volume == last {
				s.applied[c.ID] = Volume{Percent: volume}
				if err = s.a.setVolume(ctx, c.ID, s.applied[c.ID]); err != nil {
					return err
				}
			}
		}
	}
	s.volume = volume
	return nil
}

func waitUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) writePCM(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.nextWrite.IsZero() || time.Since(s.nextWrite) > 100*time.Millisecond {
		s.nextWrite = time.Now()
	}
	if err := waitUntil(ctx, s.nextWrite); err != nil {
		return err
	}
	s.conn.SetWriteDeadline(time.Now().Add(rpcTimeout))
	remaining := data
	for len(remaining) > 0 {
		n, err := s.conn.Write(remaining)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		remaining = remaining[n:]
	}
	s.nextWrite = s.nextWrite.Add(time.Duration(int64(len(data)) * int64(time.Second) / bytesPerSecond))
	return nil
}

func (s *session) silence(ctx context.Context, d time.Duration) error {
	frames := int64(d) * sampleRate / int64(time.Second)
	data := make([]byte, 960*4)
	for frames > 0 {
		n := min(frames, 960)
		if err := s.writePCM(ctx, data[:n*4]); err != nil {
			return err
		}
		frames -= n
	}
	return waitUntil(ctx, s.nextWrite)
}

func (s *session) play(ctx context.Context, job Job) error {
	f, err := os.Open(job.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 960*4)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if e := s.writePCM(ctx, buf[:n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	// Wait for the last write's nominal duration. Tail silence is sent at the
	// end of a burst, so consecutive jobs have no artificial inter-job pause.
	return waitUntil(ctx, s.nextWrite)
}

func (s *session) restore() error {
	if s == nil {
		return nil
	}
	if s.stopConn != nil {
		s.stopConn()
	}
	if s.conn != nil {
		s.conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var errs []error
	if len(s.groups) > 0 || len(s.volumes) > 0 {
		status, err := s.a.snapStatus(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("restore Snapcast status: %w", err))
		} else {
			for _, g := range status.Server.Groups {
				for _, c := range g.Clients {
					if old, ok := s.volumes[c.ID]; ok {
						// Respect external volume/mute changes made during playback.
						if c.Config.Volume == s.applied[c.ID] {
							if err = s.a.setVolume(ctx, c.ID, old); err != nil {
								errs = append(errs, err)
							}
						} else {
							slog.Info("keeping externally changed client volume", "client", c.ID)
						}
					}
				}
				if old, ok := s.groups[g.ID]; ok && g.StreamID == s.a.cfg.Snapcast.Stream {
					if err = s.a.setStream(ctx, g.ID, old); err != nil {
						errs = append(errs, err)
					}
				}
			}
		}
	}
	if s.paused {
		state, err := s.a.mpd(ctx, "status")
		if err != nil {
			errs = append(errs, err)
		} else if state["state"] == "pause" && state["songid"] == s.mpdSong && (s.mpdElapsed == "" || state["elapsed"] == s.mpdElapsed) {
			if _, err = s.a.mpd(ctx, "pause 0"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
