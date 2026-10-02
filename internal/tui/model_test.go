package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbrusegaard/hoardio/internal/report"
	"github.com/jbrusegaard/hoardio/internal/scanner"
)

const subName = "sub" // biggest child in the fixture tree

func mkTree(t *testing.T) *report.Tree {
	t.Helper()
	root := "/data"
	mk := func(rel string, size int64) scanner.File {
		return scanner.File{Path: filepath.Join(root, rel), Name: filepath.Base(rel), DiskUsage: size, ApparentSize: size}
	}
	files := []scanner.File{
		mk("big.bin", 300),
		mk("sub/a.bin", 400),
		mk("sub/deep/x.bin", 50),
		mk("small.txt", 10),
	}
	tr := report.Build(root, files)
	report.SortBySize(tr.Root)
	return tr
}

func browsedModel(t *testing.T) *Model {
	t.Helper()
	m := New()
	m.height = 24
	m.width = 80
	_, _ = m.Update(doneMsg{tree: mkTree(t), stats: scanner.Stats{FilesSeen: 4, DirsScanned: 3, BytesDisk: 460}})
	return m
}

func key(s string) tea.KeyMsg {
	m := map[string]tea.KeyType{
		"up": tea.KeyUp, "down": tea.KeyDown, "enter": tea.KeyEnter,
		"left": tea.KeyLeft, "backspace": tea.KeyBackspace, "esc": tea.KeyEsc,
	}
	if kt, ok := m[s]; ok {
		return tea.KeyMsg{Type: kt}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestNavigation(t *testing.T) {
	m := browsedModel(t)
	if len(m.stack) != 1 || m.cur().idx != 0 {
		t.Fatalf("initial stack wrong: %d/%d", len(m.stack), m.cur().idx)
	}
	if m.visible[0].Name != subName {
		t.Errorf("largest child should sort first, got %s", m.visible[0].Name)
	}

	m.goInto() // into sub/
	if len(m.stack) != 2 || m.cur().node.Name != subName {
		t.Fatalf("goInto failed: stack=%v", m.cur().node.Name)
	}
	if m.visible[0].Name != "a.bin" {
		t.Errorf("sub children order wrong: %s", m.visible[0].Name)
	}

	m.move(1) // deep/
	m.goInto()
	if m.cur().node.Name != "deep" {
		t.Fatalf("expected deep, got %s", m.cur().node.Name)
	}

	m.goUp()
	m.goUp()
	if len(m.stack) != 1 {
		t.Errorf("goUp failed: stack depth %d", len(m.stack))
	}
	m.goUp() // at root: must not pop past
	if len(m.stack) != 1 {
		t.Errorf("popped past root")
	}
}

func TestCursorClampedOnToggle(t *testing.T) {
	m := browsedModel(t)
	m.move(3) // last item (small.txt)
	m.showFiles = false
	m.refreshVisible()
	if m.cur().idx >= len(m.visible) {
		t.Errorf("cursor %d out of bounds after hiding files (%d visible)", m.cur().idx, len(m.visible))
	}
	for _, v := range m.visible {
		if !v.Dir {
			t.Error("files shown while showFiles=false")
		}
	}
}

func TestSortToggle(t *testing.T) {
	m := browsedModel(t)
	if m.visible[0].Name != subName {
		t.Fatal("precondition: size sort")
	}
	_, _ = m.Update(key("n"))
	if m.visible[0].Dir && m.visible[0].Name != subName {
		t.Errorf("name sort (dirs first) expected 'sub', got %s", m.visible[0].Name)
	}
	names := make([]string, 0, len(m.visible))
	for _, v := range m.visible {
		if !v.Dir {
			names = append(names, v.Name)
		}
	}
	if len(names) < 2 || names[0] > names[len(names)-1] {
		t.Errorf("files not alphabetical: %v", names)
	}
}

func TestQuitAndHelp(t *testing.T) {
	m := browsedModel(t)
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Error("q should return tea.Quit")
	}
	m.showHelp = true
	view := m.View()
	if !strings.Contains(view, "toggle showing files") {
		t.Error("help overlay missing from view")
	}
}

func TestViewRendersRows(t *testing.T) {
	m := browsedModel(t)
	view := m.View()
	for _, want := range []string{"sub/", "big.bin", "/data", "59.2%", "█"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) < 5 {
		t.Errorf("expected header+rows+status+footer, got %d lines", len(lines))
	}
	for _, l := range lines {
		if r := []rune(l); len(r) > m.width {
			t.Errorf("line exceeds width (%d): %q", len(r), l)
		}
	}
}

func TestScanProgressView(t *testing.T) {
	m := New()
	m.SetScanRoot("/Users/test")
	_, _ = m.Update(progressMsg{stats: scanner.Stats{DirsScanned: 5, FilesSeen: 42, BytesDisk: 1024}, elapsed: 2 * time.Second})
	view := m.View()
	if !strings.Contains(view, "/Users/test") || !strings.Contains(view, "42") {
		t.Errorf("progress view wrong:\n%s", view)
	}
}

func TestScanFailureView(t *testing.T) {
	m := New()
	_, _ = m.Update(failMsg{err: errBoom})
	if !strings.Contains(m.View(), "boom") {
		t.Error("failure view missing error")
	}
}

var errBoom = errors.New("boom")
