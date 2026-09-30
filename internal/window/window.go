//go:build !nowebview

// Package window shows the web interface in a native window using the
// system WebView (WKWebView on macOS, WebView2 on Windows, WebKitGTK on
// Linux), so pad2go runs as a desktop app from a single executable.
package window

import (
	"sync"

	webview "github.com/webview/webview_go"
)

// Window is a native window showing one URL.
type Window struct {
	w    webview.WebView
	once sync.Once
}

// New creates the window. It must be called from the main goroutine, which
// must also call Run.
func New(title, url string, width, height int) *Window {
	w := webview.New(false)
	w.SetTitle(title)
	w.SetSize(width, height, webview.HintNone)
	w.SetSize(420, 560, webview.HintMin)
	w.Navigate(url)
	return &Window{w: w}
}

// Run shows the window and blocks until it is closed.
func (w *Window) Run() {
	w.w.Run()
	w.w.Destroy()
}

// Close closes the window; safe to call from any goroutine, more than once.
func (w *Window) Close() {
	w.once.Do(func() { w.w.Dispatch(w.w.Terminate) })
}
