//go:build nowebview

// Package window, built with the nowebview tag, opens the interface in the
// default browser instead of a native window.
package window

import "sync"

// Window stands in for a native window.
type Window struct {
	done chan struct{}
	once sync.Once
}

// New opens url in the default browser.
func New(_, url string, _, _ int) *Window {
	_ = OpenBrowser(url)
	return &Window{done: make(chan struct{})}
}

// Run blocks until Close.
func (w *Window) Run() { <-w.done }

// Close releases Run.
func (w *Window) Close() { w.once.Do(func() { close(w.done) }) }
