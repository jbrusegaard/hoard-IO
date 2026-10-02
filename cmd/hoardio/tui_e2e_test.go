package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

func mkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	data := make([]byte, 8192) // non-zero to avoid APFS compression
	for i := range data {
		data[i] = byte(i)
	}
	for _, p := range []string{"a/big.bin", "b/c/deep.bin", "top.bin"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRunTUIQuitsDuringScan(t *testing.T) {
	dir := mkFixture(t)

	in, w := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runTUI(dir, scanner.Options{Workers: 2, ProgressEvery: 20 * time.Millisecond}, in, &out)
	}()

	time.Sleep(50 * time.Millisecond)
	if _, err := w.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runTUI did not exit after quit key")
	}

	got := out.String()
	if !strings.Contains(got, "Scanning") && !strings.Contains(got, "top.bin") {
		t.Errorf("expected scanning or browsing view in output:\n%q", got)
	}
}

func TestRunTUIFullSession(t *testing.T) {
	dir := mkFixture(t)

	in, w := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runTUI(dir, scanner.Options{Workers: 2, ProgressEvery: 10 * time.Millisecond}, in, &out)
	}()

	// Wait for the scan to finish (fixture is tiny), then browse and quit.
	time.Sleep(500 * time.Millisecond)
	if _, err := io.WriteString(w, "\r"); err != nil { // enter into biggest child
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := io.WriteString(w, "q"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runTUI returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runTUI did not exit")
	}

	got := out.String()
	for _, want := range []string{"top.bin", "apparent", "q quit"} { // browsing view rendered
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%q", want, got)
		}
	}
}
