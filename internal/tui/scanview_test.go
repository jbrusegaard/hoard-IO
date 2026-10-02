package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/jbrusegaard/hoardio/internal/scanner"
)

func TestSweepPosBoundsAndPingPong(t *testing.T) {
	track, bar := 20, 4

	seq := make([]int, 0, 2*(track-bar))

	for f := range 2 * (track - bar) {
		p := sweepPos(f, track, bar)
		if p < 0 || p > track-bar {
			t.Fatalf("sweepPos(%d)=%d out of [0,%d]", f, p, track-bar)
		}

		seq = append(seq, p)
	}

	if seq[0] != 0 || seq[16] == 0 {
		t.Errorf("expected motion: %v", seq[:8])
	}

	if last := seq[len(seq)-1]; last != 1 {
		t.Errorf("ping-pong should descend back, ended at %d", last)
	}

	if p := sweepPos(3, 3, 4); p != 0 {
		t.Error("degenerate track should clamp to 0")
	}
}

func TestScanCardWideHasBorder(t *testing.T) {
	m := New()
	m.SetScanRoot("/Users/test")
	m.width, m.height = 90, 24

	_, _ = m.Update(progressMsg{stats: scanner.Stats{DirsScanned: 5, FilesSeen: 42, BytesDisk: 1024}, elapsed: 2 * time.Second})

	view := strip(m.View())
	if !strings.Contains(view, "╭") || !strings.Contains(view, "/Users/test") || !strings.Contains(view, "42") {
		t.Errorf("wide scan view wrong:\n%s", view)
	}

	if strings.Contains(view, "─\n") == false && !strings.Contains(view, "────") {
		t.Error("sweep rail missing")
	}

	t.Logf("\n%s", view)
}

func TestScanCardNarrowNoBorder(t *testing.T) {
	m := New()
	m.SetScanRoot("/tmp")
	m.width = 40

	if v := m.View(); strings.Contains(v, "╭") {
		t.Error("narrow terminal should not get bordered card")
	}
}
