package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
)

func defaultWorkers() int { return 2 * runtime.GOMAXPROCS(0) }

type statT = syscall.Stat_t

func statOf(info os.FileInfo) *statT {
	st, _ := info.Sys().(*statT)
	return st
}

// devOf returns the device id as a signed wide type: darwin's st_dev is an
// int32, and widening to int64 keeps comparisons exact without any
// sign-changing cast (which would be overflow-prone).
func devOf(info os.FileInfo) int64 {
	if st := statOf(info); st != nil {
		return int64(st.Dev)
	}

	return 0
}

func inoOf(info os.FileInfo) uint64 {
	if st := statOf(info); st != nil {
		return st.Ino
	}

	return 0
}

// diskUsageOf returns bytes actually allocated on disk. Files with no stat
// data (should not happen for regular files) fall back to apparent size.
func diskUsageOf(info os.FileInfo) int64 {
	if st := statOf(info); st != nil {
		return st.Blocks * 512
	}

	return info.Size()
}

type hardkey struct {
	dev int64
	ino uint64
}

// hardlinkSet deduplicates files sharing (device, inode).
type hardlinkSet struct {
	mu  sync.Mutex
	set map[hardkey]struct{}
}

func newHardlinkSet() *hardlinkSet { return &hardlinkSet{set: make(map[hardkey]struct{})} }

// seen reports whether this identity was already observed, recording it if not.
func (h *hardlinkSet) seen(dev int64, ino uint64) bool {
	if ino == 0 { // no usable inode: cannot dedupe
		return false
	}

	k := hardkey{dev, ino}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, dup := h.set[k]; dup {
		return true
	}

	h.set[k] = struct{}{}

	return false
}

// BadPatterns returns patterns that filepath.Match rejects.
func BadPatterns(patterns []string) []string {
	var bad []string

	for _, p := range patterns {
		if _, err := filepath.Match(p, "x"); err != nil {
			if _, err2 := filepath.Match(p, "x/y"); err2 != nil {
				bad = append(bad, p)
			}
		}
	}

	return bad
}

// excluder matches paths against filepath.Match patterns. A pattern hits if it
// matches the base name ("node_modules") or the full path ("*/vendor/*").
type excluder struct {
	patterns []string
}

func newExcluder(patterns []string) *excluder {
	bad := map[string]bool{}
	for _, p := range BadPatterns(patterns) {
		bad[p] = true
	}

	e := &excluder{}

	for _, p := range patterns {
		if !bad[p] {
			e.patterns = append(e.patterns, p)
		}
	}

	return e
}

func (e *excluder) match(name, path string) bool {
	for _, p := range e.patterns {
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}

		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
	}

	return false
}
