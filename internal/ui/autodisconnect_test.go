package ui

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAutoDisconnectSettingUIAndSaveRoute(t *testing.T) {
	srv, b := serve(t)
	index := do(t, http.MethodGet, srv.URL+"/", "", nil)
	page, _ := io.ReadAll(index.Body)
	if !strings.Contains(string(page), `data-config="autoDisconnectMinutes"`) || !strings.Contains(string(page), `min="0" max="120" step="1"`) {
		t.Fatal("auto-disconnect setting control missing or unbounded")
	}
	js := do(t, http.MethodGet, srv.URL+"/app.js", "", nil)
	code, _ := io.ReadAll(js.Body)
	for _, binding := range []string{
		"const value = draft[node.dataset.config]",
		"node.value = value",
		"'autoDisconnectMinutes'",
		"Number(node.value)",
		"draft[key] =",
		"save();",
	} {
		if !strings.Contains(string(code), binding) {
			t.Fatalf("auto-disconnect control binding missing %q", binding)
		}
	}
	resp := do(t, http.MethodPut, srv.URL+"/api/config", `{"port":26760,"autoDisconnectMinutes":25}`, mutate)
	if resp.StatusCode != http.StatusOK || b.saved == nil || b.saved.AutoDisconnectMinutes != 25 {
		t.Fatalf("setting save: status %d, saved %+v", resp.StatusCode, b.saved)
	}
}
