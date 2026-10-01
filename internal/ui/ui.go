// Package ui serves the local web interface: embedded static files, a JSON
// API over the service, and server-sent events for live state and input.
//
// It listens on 127.0.0.1 only. Because any website the user visits could
// still send requests to localhost, every request must carry a matching Host
// header (against DNS rebinding) and every state-changing request must carry
// the X-Pad2go header, which browsers only allow cross-origin after a CORS
// preflight this server never approves.
package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/patrickdeangelis/pad2go/internal/app"
	"github.com/patrickdeangelis/pad2go/internal/service"
)

//go:embed web
var webFS embed.FS

// Backend is what the UI needs from the service.
type Backend interface {
	Snapshot() service.Snapshot
	Subscribe() (<-chan service.Event, func())
	SaveSettings(service.Settings) error
	Restart()
	Diagnostics() []service.Check
	Watch(player int) (<-chan app.Sample, func(), error)
	TestRumble(player int) error
	Disconnect(player int) error
}

// Server is the web interface.
type Server struct {
	b    Backend
	log  *slog.Logger
	port int
}

// Listen binds 127.0.0.1:port (0 picks a free port).
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// Handler returns the HTTP handler for a server reachable on port.
func Handler(b Backend, port int, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{b: b, log: log, port: port}
	static, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("PUT /api/config", s.saveConfig)
	mux.HandleFunc("POST /api/restart", s.restart)
	mux.HandleFunc("GET /api/diagnostics", s.diagnostics)
	mux.HandleFunc("GET /api/players/{n}/input", s.input)
	mux.HandleFunc("POST /api/players/{n}/rumble", s.rumble)
	mux.HandleFunc("POST /api/players/{n}/disconnect", s.disconnect)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	allowed := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.port): true,
		fmt.Sprintf("localhost:%d", s.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Pad2go") != "1" {
				http.Error(w, "missing X-Pad2go header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.b.Snapshot())
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	var set service.Settings
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&set); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid settings: %w", err))
		return
	}
	if err := s.b.SaveSettings(set); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, s.b.Snapshot())
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	s.b.Restart()
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.b.Diagnostics())
}

func player(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 || n > 8 {
		return 0, errors.New("invalid player")
	}
	return n, nil
}

func (s *Server) playerAction(w http.ResponseWriter, r *http.Request, fn func(int) error) {
	n, err := player(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := fn(n); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rumble(w http.ResponseWriter, r *http.Request) {
	s.playerAction(w, r, s.b.TestRumble)
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	s.playerAction(w, r, s.b.Disconnect)
}

// sse prepares a server-sent events response.
func sse(w http.ResponseWriter) (func(event string, v any) error, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return func(event string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}, true
}

// events streams "state" snapshots (on change, coalesced to 10/s, and every
// 2 s for battery and DSU client counts) and "toast" connection notices.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	send, ok := sse(w)
	if !ok {
		return
	}
	events, cancel := s.b.Subscribe()
	defer cancel()
	if send("state", s.b.Snapshot()) != nil {
		return
	}
	refresh := time.NewTicker(2 * time.Second)
	defer refresh.Stop()
	coalesce := time.NewTicker(100 * time.Millisecond)
	defer coalesce.Stop()
	dirty := false
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			if e.Type == "toast" {
				if send("toast", e.Toast) != nil {
					return
				}
			} else {
				dirty = true
			}
		case <-coalesce.C:
			if dirty {
				dirty = false
				if send("state", s.b.Snapshot()) != nil {
					return
				}
			}
		case <-refresh.C:
			if send("state", s.b.Snapshot()) != nil {
				return
			}
		}
	}
}

// inputSample is one test-panel frame. Sticks are -1..1 (+Y up), triggers
// 0-255 and gyro in deg/s.
type inputSample struct {
	Buttons uint16     `json:"buttons"`
	LX      float64    `json:"lx"`
	LY      float64    `json:"ly"`
	RX      float64    `json:"rx"`
	RY      float64    `json:"ry"`
	LT      uint8      `json:"lt"`
	RT      uint8      `json:"rt"`
	Analog  bool       `json:"analog"`
	Gyro    [3]float32 `json:"gyro"`
}

// input streams a player's input at up to 30 frames per second, sending only
// the latest sample each frame.
func (s *Server) input(w http.ResponseWriter, r *http.Request) {
	n, err := player(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	samples, cancel, err := s.b.Watch(n)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	defer cancel()
	send, ok := sse(w)
	if !ok {
		return
	}
	frame := time.NewTicker(33 * time.Millisecond)
	defer frame.Stop()
	var latest *app.Sample
	for {
		select {
		case <-r.Context().Done():
			return
		case smp := <-samples:
			latest = &smp
		case <-frame.C:
			if latest == nil {
				continue
			}
			x := latest.Xbox
			if send("input", inputSample{
				Buttons: x.Buttons, LX: axis(x.LX), LY: axis(x.LY), RX: axis(x.RX), RY: axis(x.RY),
				LT: x.LeftTrigger, RT: x.RightTrigger, Analog: latest.AnalogTriggers, Gyro: latest.Gyro,
			}) != nil {
				return
			}
			latest = nil
		}
	}
}

func axis(v int16) float64 { return float64(v) / 32767 }
