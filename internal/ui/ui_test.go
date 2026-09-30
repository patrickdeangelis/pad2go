package ui

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/angelispatrick/pad2go/internal/app"
	"github.com/angelispatrick/pad2go/internal/mapping"
	"github.com/angelispatrick/pad2go/internal/service"
)

type fakeBackend struct {
	saved     *service.Settings
	restarted bool
	rumbled   int
	samples   chan app.Sample
}

func (f *fakeBackend) Snapshot() service.Snapshot {
	return service.Snapshot{Platform: "Linux", Players: []service.PlayerView{}}
}
func (f *fakeBackend) Subscribe() (<-chan service.Event, func()) {
	return make(chan service.Event), func() {}
}
func (f *fakeBackend) SaveSettings(s service.Settings) error {
	if s.Port == 0 {
		return errors.New("cemuhook_port: want 1-65535, got 0")
	}
	f.saved = &s
	return nil
}
func (f *fakeBackend) Restart() { f.restarted = true }
func (f *fakeBackend) Diagnostics() []service.Check {
	return []service.Check{{Name: "Bluetooth", OK: true}}
}
func (f *fakeBackend) Watch(n int) (<-chan app.Sample, func(), error) {
	if n != 1 {
		return nil, nil, app.ErrNoPlayer
	}
	return f.samples, func() {}, nil
}
func (f *fakeBackend) TestRumble(n int) error {
	if n != 1 {
		return app.ErrNoPlayer
	}
	f.rumbled++
	return nil
}
func (f *fakeBackend) Disconnect(int) error { return nil }

func serve(t *testing.T) (*httptest.Server, *fakeBackend) {
	t.Helper()
	b := &fakeBackend{samples: make(chan app.Sample, 1)}
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	srv.Config.Handler = Handler(b, port, nil)
	t.Cleanup(srv.Close)
	return srv, b
}

func do(t *testing.T, method, url string, body string, header map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range header {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

var mutate = map[string]string{"X-Pad2go": "1"}

func TestGuards(t *testing.T) {
	srv, b := serve(t)
	cases := []struct {
		name   string
		method string
		header map[string]string
		want   int
	}{
		{"foreign host (DNS rebinding)", "GET", map[string]string{"Host": "evil.example:80"}, http.StatusForbidden},
		{"state read", "GET", nil, http.StatusOK},
		{"mutation without header", "POST", nil, http.StatusForbidden},
		{"cross-origin mutation", "POST", map[string]string{"X-Pad2go": "1", "Origin": "http://evil.example"}, http.StatusForbidden},
		{"same-origin mutation", "POST", map[string]string{"X-Pad2go": "1", "Origin": srv.URL}, http.StatusAccepted},
	}
	for _, c := range cases {
		path := "/api/state"
		if c.method == "POST" {
			path = "/api/restart"
		}
		if got := do(t, c.method, srv.URL+path, "", c.header).StatusCode; got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	if !b.restarted {
		t.Fatal("restart not forwarded")
	}
}

func TestStaticAndSecurityHeaders(t *testing.T) {
	srv, _ := serve(t)
	resp := do(t, "GET", srv.URL+"/", "", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>Pad2Go</title>") {
		t.Fatalf("index: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("headers %v", resp.Header)
	}
	for _, asset := range []string{"/app.js", "/styles.css", "/assets/pad2go-icon.png"} {
		if code := do(t, "GET", srv.URL+asset, "", nil).StatusCode; code != 200 {
			t.Errorf("%s: %d", asset, code)
		}
	}
}

func TestSaveConfig(t *testing.T) {
	srv, b := serve(t)
	if code := do(t, "PUT", srv.URL+"/api/config", "{nope", mutate).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("malformed JSON: %d", code)
	}
	if code := do(t, "PUT", srv.URL+"/api/config", `{"port":1,"surprise":true}`, mutate).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
	resp := do(t, "PUT", srv.URL+"/api/config", `{"port":0}`, mutate)
	var e map[string]string
	json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(e["error"], "cemuhook_port") {
		t.Fatalf("invalid: %d %v", resp.StatusCode, e)
	}
	if code := do(t, "PUT", srv.URL+"/api/config", `{"port":26760,"layout":"Switch"}`, mutate).StatusCode; code != 200 || b.saved.Layout != "Switch" {
		t.Fatalf("valid: %d", code)
	}
}

func TestPlayerRoutes(t *testing.T) {
	srv, b := serve(t)
	if code := do(t, "POST", srv.URL+"/api/players/1/rumble", "", mutate).StatusCode; code != http.StatusNoContent || b.rumbled != 1 {
		t.Fatalf("rumble: %d", code)
	}
	if code := do(t, "POST", srv.URL+"/api/players/2/rumble", "", mutate).StatusCode; code != http.StatusNotFound {
		t.Fatalf("empty slot: %d", code)
	}
	if code := do(t, "POST", srv.URL+"/api/players/x/rumble", "", mutate).StatusCode; code != http.StatusBadRequest {
		t.Fatalf("bad player: %d", code)
	}
	if code := do(t, "GET", srv.URL+"/api/players/2/input", "", nil).StatusCode; code != http.StatusNotFound {
		t.Fatalf("input for empty slot: %d", code)
	}
}

func TestInputStream(t *testing.T) {
	srv, b := serve(t)
	b.samples <- app.Sample{Xbox: mapping.XboxState{Buttons: mapping.XBA, LX: 32767, RightTrigger: 255}, AnalogTriggers: true, Gyro: [3]float32{1, 2, 3}}
	resp := do(t, "GET", srv.URL+"/api/players/1/input", "", nil)
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case line := <-lines:
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var s inputSample
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &s); err != nil {
				t.Fatal(err)
			}
			if s.Buttons != mapping.XBA || s.LX != 1 || s.RT != 255 || !s.Analog || s.Gyro != [3]float32{1, 2, 3} {
				t.Fatalf("sample %+v", s)
			}
			return
		case <-timeout:
			t.Fatal("no input event")
		}
	}
}
