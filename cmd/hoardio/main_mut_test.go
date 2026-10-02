package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

var errDead = errors.New("dead writer")

type deadWriter struct{}

func (deadWriter) Write([]byte) (int, error) { return 0, errDead }

func sampleResult() (res *scanner.Result, excludes []string) {
	root := "/rep"
	res = &scanner.Result{
		Root: root,
		Files: []scanner.File{
			{Path: root + "/sub/big.bin", Name: "big.bin", DiskUsage: 500, ApparentSize: 500},
			{Path: root + "/small.txt", Name: "small.txt", DiskUsage: 20, ApparentSize: 20},
		},
		Errors: []scanner.DirError{{Path: "/rep/locked", Err: errDead}},
		Stats:  scanner.Stats{DirsScanned: 2, FilesSeen: 2, BytesDisk: 520},
	}

	return res, []string{"[bad"}
}

func TestRenderReportTextWarnsAndNotes(t *testing.T) {
	res, excludes := sampleResult()

	var out, warn bytes.Buffer

	tf := textFlags{top: 5, depth: 2, excludes: excludes}

	err := renderReport(&out, &warn, res, tf, 1234*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, want := range []string{"Scan of /rep", "big.bin", "in 1.23"} {
		if !strings.Contains(got, want) {
			t.Errorf("text report missing %q:\n%s", want, got)
		}
	}

	w := warn.String()
	if !strings.Contains(w, `invalid --exclude pattern "[bad"`) {
		t.Errorf("warn missing bad-pattern note: %q", w)
	}
}

func TestRenderReportInterruptedNote(t *testing.T) {
	res, _ := sampleResult()
	res.Err = errDead

	var out, warn bytes.Buffer

	if err := renderReport(&out, &warn, res, textFlags{top: 1, depth: 1}, time.Second); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(warn.String(), "scan interrupted") {
		t.Errorf("missing interruption note, warn=%q", warn.String())
	}
}

func TestRenderReportJSON(t *testing.T) {
	res, _ := sampleResult()

	var out bytes.Buffer

	tf := textFlags{json: true}

	if err := renderReport(&out, &bytes.Buffer{}, res, tf, time.Second); err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := doc["tree"]; !ok {
		t.Error("json report missing tree")
	}
}

func TestRenderReportWriterErrors(t *testing.T) {
	res, _ := sampleResult()

	if err := renderReport(deadWriter{}, &bytes.Buffer{}, res, textFlags{top: 1, depth: 1}, time.Second); !errors.Is(err, errDead) {
		t.Errorf("text mode: want %v wrapped, got %v", errDead, err)
	}

	if err := renderReport(deadWriter{}, &bytes.Buffer{}, res, textFlags{json: true}, time.Second); !errors.Is(err, errDead) {
		t.Errorf("json mode: want %v wrapped, got %v", errDead, err)
	}
}

func TestResolveRoot(t *testing.T) {
	got, err := resolveRoot("relative", []string{"relative"})
	if err != nil {
		t.Fatal(err)
	}

	if !filepath.IsAbs(got) {
		t.Errorf("want absolute, got %s", got)
	}

	t.Setenv("HOME", "/fake-home")

	got, err = resolveRoot("", []string{})
	if err != nil {
		t.Fatal(err)
	}

	if got != "/fake-home" {
		t.Errorf("empty arg should default to $HOME, got %s", got)
	}

	if _, err := resolveRoot("/a", []string{"/a", "/b"}); !errors.Is(err, errUsage) {
		t.Errorf("two args: want errUsage, got %v", err)
	}
}

func TestExitCode(t *testing.T) {
	if got := exitCode(errUsage); got != usageExit {
		t.Errorf("exitCode(errUsage)=%d want %d", got, usageExit)
	}

	if got := exitCode(errors.New("boom")); got != 1 {
		t.Errorf("exitCode(other)=%d want 1", got)
	}
}

func TestExcludeDirsFlag(t *testing.T) {
	var e excludedirs

	if err := e.Set("a"); err != nil || e.String() != "a" {
		t.Fatalf("Set/String: %v %q", err, e.String())
	}

	if err := e.Set("b"); err != nil {
		t.Fatal(err)
	}

	if e.String() != "a,b" || len(e) != 2 {
		t.Errorf("repeatable flag broken: %q", e.String())
	}
}

func TestIsTerminalOnRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if isTerminal(f) {
		t.Error("regular file must not report as terminal")
	}

	if !isTerminal(os.Stdin) && os.Getenv("CI") == "" {
		t.Log("stdin is not a terminal in test env; false-branch covered")
	}
}
