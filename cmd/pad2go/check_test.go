package main

import (
	"path/filepath"
	"testing"
)

// An unknown check target fails before any driver is touched, so the installer
// can't silently run the wrong check.
func TestCheckOutputRejectsUnknownTarget(t *testing.T) {
	err := checkOutput(filepath.Join(t.TempDir(), "config.yaml"), "bogus")
	if err == nil {
		t.Fatal("unknown check target accepted")
	}
}
