package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/jbrusegaard/hoardio/internal/report"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func strip(s string) string { return ansiRE.ReplaceAllString(s, "") }

func TestSizeBarExactWidthAndFill(t *testing.T) {
	cases := []struct {
		frac   float64
		w      int
		filled int
	}{
		{0, 5, 0},
		{1, 5, 5},
		{0.5, 5, 3},
		{2.5, 4, 4},
		{-1, 4, 0},
	}

	for _, c := range cases {
		got := strip(sizeBar(c.frac, c.w))
		if runewidth.StringWidth(got) != c.w {
			t.Errorf("sizeBar(%v,%d) width=%d (%q)", c.frac, c.w, runewidth.StringWidth(got), got)
		}

		if n := strings.Count(got, "█"); n != c.filled {
			t.Errorf("sizeBar(%v,%d) filled=%d want %d (%q)", c.frac, c.w, n, c.filled, got)
		}
	}
}

func TestHeatStyleBoundaries(t *testing.T) {
	sentinel := "X"

	cases := []struct {
		frac float64
		want lipStyle
	}{
		{0.67, hotStyle},
		{0.66, warmStyle},
		{0.34, warmStyle},
		{0.33, coolStyle},
	}

	for _, c := range cases {
		if heatStyle(c.frac).Render(sentinel) != c.want.Render(sentinel) {
			t.Errorf("heatStyle(%v) picked wrong band", c.frac)
		}
	}
}

type lipStyle = interface{ Render(...string) string }

func TestTruncateEdges(t *testing.T) {
	if got := truncate("abc", 5); got != "abc" {
		t.Errorf("short string altered: %q", got)
	}

	if got := runewidth.StringWidth(truncate("abcdefgh", 4)); got > 4 {
		t.Errorf("truncated width=%d want <=4", got)
	}

	if got := truncate("abcd", 1); got != "…" {
		t.Errorf("w=1: %q", got)
	}

	if got := truncate("x", 0); got != "" {
		t.Errorf("w=0: %q", got)
	}
}

func TestPctOfParentZeroTotal(t *testing.T) {
	m := browsedModel(t)
	m.cur().node.DiskUsage = 0

	if got := m.pctOfParent(&report.Node{DiskUsage: 5}); got != 0 {
		t.Errorf("pctOfParent with zero parent=%v want 0", got)
	}
}

func TestLargestChildEmpty(t *testing.T) {
	if got := largestChild(nil); got != 0 {
		t.Errorf("largestChild(nil)=%d want 0", got)
	}
}

func TestRenderRowZeroMaxSizeNoPanic(t *testing.T) {
	m := browsedModel(t)

	row := strip(m.renderRow(&report.Node{Name: "ghost", DiskUsage: 0}, 0, 80, true))
	if !strings.Contains(row, "0.0%") || !strings.Contains(row, "▸") {
		t.Errorf("row=%q want zero-pct selected row with cursor", row)
	}
}

func TestShowFilesToggle(t *testing.T) {
	m := browsedModel(t)

	withFiles := len(m.visible)
	if withFiles < 2 {
		t.Fatalf("fixture should list several entries, got %d", withFiles)
	}

	m.Update(key("f"))

	without := len(m.visible)

	if without >= withFiles {
		t.Errorf("hide files: %d -> %d entries (want fewer)", withFiles, without)
	}

	m.Update(key("f"))

	if len(m.visible) != withFiles {
		t.Errorf("restore files: got %d want %d", len(m.visible), withFiles)
	}
}

func TestHelpOverlayToggles(t *testing.T) {
	m := browsedModel(t)

	if strings.Contains(strip(m.View()), "move cursor") {
		t.Error("help shown before toggle")
	}

	m.Update(key("?"))

	if !strings.Contains(strip(m.View()), "move cursor") {
		t.Error("help missing after '?'")
	}

	m.Update(key("?"))

	if strings.Contains(strip(m.View()), "move cursor") {
		t.Error("help still shown after second '?'")
	}
}

func TestBrowserEmptyDirShowsPlaceholder(t *testing.T) {
	m := browsedModel(t)
	m.visible = nil

	if v := strip(m.viewBrowser()); !strings.Contains(v, "(empty)") {
		t.Errorf("empty dir view=%q want (empty)", v)
	}
}

func TestStatusLineTruncatesToWidth(t *testing.T) {
	m := browsedModel(t)
	m.width = 20

	if w := runewidth.StringWidth(m.statusLine()); w > 20 {
		t.Errorf("status width=%d want <=20 (%q)", w, m.statusLine())
	}
}
