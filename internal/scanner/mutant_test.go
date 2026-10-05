package scanner

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressWriterAndCallbackFire(t *testing.T) {
	var buf bytes.Buffer

	var ticks atomic.Int64

	c := &counters{}
	c.files.Add(7)
	c.bytes.Add(2048)

	stop := startProgress(context.Background(), &buf, func(s Stats) { ticks.Add(1) }, time.Millisecond, c)

	deadline := time.Now().Add(time.Second)
	for ticks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	stop()

	if ticks.Load() == 0 {
		t.Error("OnProgress callback never fired")
	}

	if !strings.Contains(buf.String(), "scanned") {
		t.Errorf("progress writer got %q", buf.String())
	}
}

func TestProgressCancelStopsTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	c := &counters{}

	stop := startProgress(ctx, nil, nil, time.Millisecond, c)

	cancel()
	time.Sleep(20 * time.Millisecond)

	stop() // must return: ticker goroutine exits on ctx cancel
}

func TestStartProgressNoopWhenBothSinksNil(t *testing.T) {
	c := &counters{}
	if got := startProgress(context.Background(), nil, nil, 0, c); got == nil {
		t.Fatal("startProgress returned nil stop func")
	} else {
		got()
	}
}

func TestTickProgressPartialSinks(t *testing.T) {
	c := &counters{}
	c.files.Add(3)

	var fired int

	tickProgress(func(s Stats) { fired++ }, nil, c)

	if fired != 1 {
		t.Errorf("fn-only tick: fired=%d want 1", fired)
	}

	var buf bytes.Buffer

	tickProgress(nil, &buf, c) // must not panic with nil fn

	if !strings.Contains(buf.String(), "scanned 0 dirs, 3 files") {
		t.Errorf("writer-only tick got %q", buf.String())
	}
}

func TestDefaultWorkersPositive(t *testing.T) {
	if w := defaultWorkers(); w < 1 {
		t.Errorf("defaultWorkers()=%d, want >= 1", w)
	}
}

func TestExcluderBaseAndFullPath(t *testing.T) {
	e := newExcluder([]string{"node_modules", "/a/vendor/*"})

	if !e.match("node_modules", "/a/node_modules") {
		t.Error("base-name pattern should match")
	}

	if !e.match("x", "/a/vendor/x") {
		t.Error("full-path pattern should match")
	}

	if e.match("x", "/a/keep/x") {
		t.Error("unrelated path should not match")
	}
}

func TestBadPatternsOnlyTrulyBroken(t *testing.T) {
	bad := BadPatterns([]string{"[", "ok*", "*.tmp"})
	if len(bad) != 1 || bad[0] != "[" {
		t.Errorf("BadPatterns=%v, want exactly [", bad)
	}

	if got := BadPatterns(nil); got != nil {
		t.Errorf("BadPatterns(nil)=%v, want nil", got)
	}
}

func TestHumanBytesBoundaries(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{-5, "-5 B"},
		{1 << 30, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.n); got != c.want {
			t.Errorf("HumanBytes(%d)=%q want %q", c.n, got, c.want)
		}
	}
}
